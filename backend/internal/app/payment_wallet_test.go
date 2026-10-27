package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/payment"
)

type storageCheckoutProvider struct {
	payment.Provider
	descriptor payment.Descriptor
	calls      int
}

func (p *storageCheckoutProvider) Descriptor() payment.Descriptor { return p.descriptor }

func (p *storageCheckoutProvider) CreateOrder(_ context.Context, _ payment.Config, request payment.CreateRequest) (payment.Checkout, error) {
	p.calls++
	return payment.Checkout{Mode: "redirect", Value: "https://pay.example/checkout", ExpiresAt: request.ExpiresAt}, nil
}

func TestExternalWalletCreatesStorageCheckoutAndRejectsCreditCheckout(t *testing.T) {
	s, db, _, user := newSharedWalletTest(t)
	if err := db.AutoMigrate(&model.TopupProduct{}, &model.PaymentProviderConfig{}, &model.PluginPlatformState{}); err != nil {
		t.Fatal(err)
	}
	manifest := bundledPaymentPluginManifests()[0]
	manifest.Metadata.Enabled = true
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	s.pluginRuntime = &pluginRuntime{plugins: map[string]pluginRecord{manifest.Metadata.ID: {Raw: raw, Metadata: manifest.Metadata, Source: PluginOriginOfficial}}}
	provider := &storageCheckoutProvider{descriptor: payment.DescriptorFromManifest(manifest, manifest.Contributes.PaymentProviders[0])}
	s.paymentRegistry, err = payment.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.PluginPlatformState{PluginID: manifest.Metadata.ID, Available: true}).Error; err != nil {
		t.Fatal(err)
	}
	cipher, err := s.encryptSettingSecret(`{"publicBaseUrl":"https://canvas.example"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.PaymentProviderConfig{ID: "payment-config", ProviderID: provider.descriptor.ID, PluginID: provider.descriptor.PluginID, Version: 1, Enabled: true, ConfigCipher: cipher, CloseAfterMinutes: 30}).Error; err != nil {
		t.Fatal(err)
	}
	for _, product := range []model.TopupProduct{
		{ID: "storage", Kind: model.TopupProductKindStorage, Enabled: true, AmountFen: 100, StorageBytes: 1024, StorageDurationDays: 30},
		{ID: "credits", Kind: model.TopupProductKindCredits, Enabled: true, AmountFen: 100, CreditsMicrocredits: 1_000_000},
	} {
		if err := db.Create(&product).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := CreatePaymentOrderRequest{ProductID: "storage", ProviderID: provider.descriptor.ID, IdempotencyKey: "storage-purchase"}
	order, err := s.CreatePaymentOrder(context.Background(), user, request)
	if err != nil || order == nil || order.ProductKind != model.TopupProductKindStorage || order.Status != model.PaymentOrderPending || order.StorageBytes != 1024 {
		t.Fatalf("storage checkout: order=%+v err=%v", order, err)
	}
	if err := db.Model(&model.TopupProduct{}).Where("id = ?", "storage").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	retry, err := s.CreatePaymentOrder(context.Background(), user, request)
	if err != nil || retry.ID != order.ID || provider.calls != 1 {
		t.Fatalf("idempotent checkout: order=%+v calls=%d err=%v", retry, provider.calls, err)
	}
	_, err = s.CreatePaymentOrder(context.Background(), user, CreatePaymentOrderRequest{ProductID: "credits", ProviderID: provider.descriptor.ID, IdempotencyKey: "credit-purchase"})
	if err == nil || !strings.Contains(err.Error(), "外部钱包") || provider.calls != 1 {
		t.Fatalf("credit checkout bypassed wallet restriction: calls=%d err=%v", provider.calls, err)
	}
	old := model.PaymentOrder{ID: "old-credit", UserID: user.ID, IdempotencyKey: "old-credit", MerchantOrderNo: "old-credit", ProductID: "credits", ProductKind: model.TopupProductKindCredits, ProviderID: provider.descriptor.ID, Status: model.PaymentOrderCredited, CreditsMicrocredits: 1_000_000}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.TopupProduct{}).Where("id = ?", "credits").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	replayed, err := s.CreatePaymentOrder(context.Background(), user, CreatePaymentOrderRequest{ProductID: "credits", ProviderID: provider.descriptor.ID, IdempotencyKey: "old-credit"})
	if err != nil || replayed.ID != old.ID || replayed.Status != model.PaymentOrderCredited || provider.calls != 1 {
		t.Fatalf("historical credit replay: order=%+v calls=%d err=%v", replayed, provider.calls, err)
	}
}
