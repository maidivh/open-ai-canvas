package repository

import (
	"errors"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func TestExternalWalletPaymentAdmissionUsesStoredProductKind(t *testing.T) {
	for _, kind := range []model.TopupProductKind{model.TopupProductKindStorage, model.TopupProductKindCredits, ""} {
		name := string(kind)
		if name == "" {
			name = "legacy_credits"
		}
		t.Run(name, func(t *testing.T) {
			db := openPaymentTestDB(t)
			if err := db.AutoMigrate(&model.SystemSetting{}, &model.UserStorageGrant{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.SystemSetting{Key: model.SharedWalletSettingKey, ValueJSON: `{"mode":"external"}`}).Error; err != nil {
				t.Fatal(err)
			}
			product := model.TopupProduct{ID: "product", Kind: kind, Enabled: true, AmountFen: 100, CreditsMicrocredits: 100_000_000, StorageBytes: 1024, StorageDurationDays: 30, SaleStrategy: model.TopupSaleStrategyInventory, StockTotal: 1, StockRemaining: 1}
			if err := db.Create(&product).Error; err != nil {
				t.Fatal(err)
			}
			repo := New(db)
			order := paymentLimitTestOrder("purchase", product.ID)
			order.ProductKind = model.TopupProductKindStorage
			if kind == model.TopupProductKindStorage {
				// A stale or forged order snapshot must not turn storage into local credits.
				order.ProductKind = model.TopupProductKindCredits
				if _, _, err := repo.CreatePaymentOrderWithProductReservation(order); !errors.Is(err, ErrTopupUnavailable) {
					t.Fatalf("mismatched product snapshot was accepted: %v", err)
				}
				order.ProductKind = model.TopupProductKindStorage
			}
			saved, created, err := repo.CreatePaymentOrderWithProductReservation(order)
			if kind != model.TopupProductKindStorage {
				if err == nil || created {
					t.Fatalf("external wallet accepted credit product: created=%v err=%v", created, err)
				}
				product, err := repo.TopupProduct(product.ID)
				if err != nil || product.StockRemaining != 1 {
					t.Fatalf("rejected purchase consumed stock: product=%+v err=%v", product, err)
				}
				return
			}
			if err != nil || !created || saved.ProductKind != model.TopupProductKindStorage {
				t.Fatalf("storage admission: order=%+v created=%v err=%v", saved, created, err)
			}
			retry, created, err := repo.CreatePaymentOrderWithProductReservation(order)
			if err != nil || created || retry.ID != saved.ID {
				t.Fatalf("sold-out idempotent retry: order=%+v created=%v err=%v", retry, created, err)
			}
			for range 2 {
				if _, _, err := repo.CompletePaymentOrder(saved.ProviderID, saved.MerchantOrderNo, PaymentEvidence{ProviderTradeNo: "storage-trade", AmountFen: 100, Currency: "CNY", PaidAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}
			var grants []model.UserStorageGrant
			if err := db.Find(&grants).Error; err != nil || len(grants) != 1 || grants[0].Bytes != 1024 {
				t.Fatalf("storage entitlement: grants=%+v err=%v", grants, err)
			}
			var credits int64
			if err := db.Model(&model.CreditLedgerEntry{}).Count(&credits).Error; err != nil || credits != 0 {
				t.Fatalf("storage purchase granted local credits: count=%d err=%v", credits, err)
			}
		})
	}
}

func TestPaymentAdmissionRejectsChangedProductKind(t *testing.T) {
	db := openPaymentTestDB(t)
	product := model.TopupProduct{ID: "changed", Kind: model.TopupProductKindCredits, Enabled: true, AmountFen: 100, CreditsMicrocredits: 1_000_000}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	order := paymentLimitTestOrder("stale", product.ID)
	order.ProductKind = model.TopupProductKindStorage
	order.CreditsMicrocredits = 0
	order.StorageBytes = 1024
	if _, _, err := New(db).CreatePaymentOrderWithProductReservation(order); !errors.Is(err, ErrTopupUnavailable) {
		t.Fatalf("changed product kind accepted stale zero-credit order: %v", err)
	}
	var count int64
	if err := db.Model(&model.PaymentOrder{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("changed product created a payment order: count=%d err=%v", count, err)
	}
}

func TestExternalWalletReplaysExistingCreditPaymentOrder(t *testing.T) {
	db := openPaymentTestDB(t)
	if err := db.AutoMigrate(&model.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	product := model.TopupProduct{ID: "credits", Kind: model.TopupProductKindCredits, Enabled: true}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	order := paymentLimitTestOrder("old", product.ID)
	if _, _, err := repo.CreatePaymentOrderWithProductReservation(order); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.SystemSetting{}).Where("key = ?", model.SharedWalletSettingKey).Update("value_json", `{"mode":"external"}`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&product).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	saved, created, err := repo.CreatePaymentOrderWithProductReservation(order)
	if err != nil || created || saved.ID != order.ID || saved.CreditsMicrocredits != 100_000_000 {
		t.Fatalf("old credit order replay: order=%+v created=%v err=%v", saved, created, err)
	}
}
