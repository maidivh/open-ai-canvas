package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

func TestSharedWalletAuthorizationCoversCacheWriteAndContextRules(t *testing.T) {
	s, db, upstream, user := newSharedWalletTest(t)
	if err := db.AutoMigrate(&model.ChannelModel{}, &model.ChannelModelPriceTier{}); err != nil {
		t.Fatal(err)
	}
	item := model.ChannelModel{
		ID: "text-model", ChannelID: "channel", ModelKey: "text-model", Capability: "text",
		Protocol: model.ChannelInterfaceChatCompletion, Enabled: true, PriceCurrency: model.PriceCurrencyCredits,
		CapabilityConfigJSON: `{"text":{"contextWindowTokens":128000,"maxOutputTokens":16384}}`,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	tier := model.ChannelModelPriceTier{
		ID: "text-tier", ChannelModelID: item.ID, SelectorKey: "{}", SelectorJSON: "{}",
		BillingMode: "token", Enabled: true, PriceConfigured: true,
		InputTokenPriceMicrocredits: 10_000_000, OutputTokenPriceMicrocredits: 20_000_000,
		CacheWriteTokenPriceMicrocredits: 30_000_000, TokenPriceRulesEnabled: true,
		TokenPriceRules: []model.TokenPriceRule{{
			Enabled: true, Weekdays: []int{1, 2, 3, 4, 5, 6, 7}, Start: "00:00", End: "24:00", MaxInputTokens: 64000,
			InputTokenPriceMicrocredits: 40_000_000, OutputTokenPriceMicrocredits: 50_000_000,
			CachedTokenPriceMicrocredits: 60_000_000, CacheWriteTokenPriceMicrocredits: 70_000_000,
		}},
	}
	if err := db.Create(&tier).Error; err != nil {
		t.Fatal(err)
	}
	order, err := s.newBillingOrderWithPriceTierAt(time.Now(), user.ID, "", "proxy", item.ChannelID, item.ModelKey, "text", "text", 1, estimateTaskTokens(map[string]any{"prompt": "hello"}), tier.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 上下文上限不命中贵价档，但实际输入可能命中；冻结金额必须同时覆盖缓存写入和输出。
	if order.AmountMicrocredits != 10_000_000 || order.ChargeLimitMicrocredits != order.AmountMicrocredits || !order.ChargeLimitSet {
		t.Fatalf("authorization did not cover all candidate prices: %#v", order)
	}
	if order.WalletOutputTokenLimit != 16384 || order.CacheWriteTokenPriceMicrocredits != tier.CacheWriteTokenPriceMicrocredits {
		t.Fatalf("wallet limit or base cache-write price lost: %#v", order)
	}
	rules, err := order.BillingTokenPriceRules()
	if err != nil || len(rules) != 1 || rules[0].CacheWrite != 70_000_000 {
		t.Fatalf("settlement rule snapshot = %#v, %v", rules, err)
	}
	if err := s.repo.ReserveBillingOrder(order); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBillingRunning(order.ID); err != nil {
		t.Fatal(err)
	}
	usage := model.ApiCallLog{
		ID: "usage", BillingOrderID: order.ID, RequestKind: "generate", Billable: true,
		Status: model.ApiCallStatusSucceeded, UsageAvailable: true,
		InputTokens: 50000, OutputTokens: 1000, CachedTokens: 5000, CacheWriteTokens: 40000,
	}
	if err := db.Create(&usage).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.SettleBilling(order.ID, ""); err != nil {
		t.Fatal(err)
	}
	saved, err := s.repo.BillingOrder(order.ID)
	if err != nil || saved.TokenPriceRule != 1 || saved.CacheWriteTokens != 40000 || saved.ActualAmountMicrocredits != 4_000_000 || saved.Status != model.BillingStatusSettled {
		t.Fatalf("external settlement lost rule or cache-write usage: %#v, %v", saved, err)
	}
	if upstream.balance != 96 {
		t.Fatalf("wallet balance = %d, want 96", upstream.balance)
	}
}

func TestSharedWalletDeclarativeThinkingUsesFrozenBudget(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		thinking, _ := body["thinking"].(map[string]any)
		if body["max_tokens"] != float64(4096) || thinking["budget_tokens"] != float64(1638) || body["max_output_tokens"] != nil {
			t.Errorf("thinking and output limits diverged: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"done"}]}`))
	}))
	defer server.Close()
	ctx := withProtocolRegistry(context.Background(), loadOfficialFallbackRegistry())
	input := canvasGenerationInput{
		Mode: "text", MaxOutputTokens: 4096, EnforceWalletOutputLimit: true,
		TextOptions: canvasTextOptions{ThinkingLevel: "medium"},
		Config: providerConfig{BaseURL: server.URL, InterfaceType: "claude-api",
			CapabilityConfig: &ModelCapabilityConfig{Text: &TextCapabilityConfig{Thinking: mustThinkingPreset(t, "claude-budget", protocol.TextWireClaude)}}},
	}
	_, _, err := executeProtocolCreateRequest(ctx, input, protocol.RequestSpec{
		Method: "POST", Path: "/messages", ContentType: "application/json",
		Body: map[string]any{"max_tokens": 64000, "max_output_tokens": 64000},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSharedWalletDeclarativeAgentThinkingCannotReopenOutputLimit(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["max_tokens"] != float64(512) {
			t.Errorf("non-streaming Agent reapplied thinking after wallet limit: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"done"}]}`))
	}))
	defer server.Close()
	ctx := withProtocolRegistry(context.Background(), loadOfficialFallbackRegistry())
	_, err := runAgentToolTask(ctx, canvasGenerationInput{
		Mode: "text", MaxOutputTokens: 512, EnforceWalletOutputLimit: true,
		TextOptions: canvasTextOptions{ThinkingLevel: "medium"},
		Config: providerConfig{BaseURL: server.URL, InterfaceType: "claude-api", Model: "claude",
			CapabilityConfig: &ModelCapabilityConfig{Text: &TextCapabilityConfig{Thinking: mustThinkingPreset(t, "claude-budget", protocol.TextWireClaude)}}},
		AgentRequests: &agentToolRequests{Claude: map[string]any{"messages": []any{}, "tools": []any{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSharedWalletVideoSettlementPreservesReferenceSeconds(t *testing.T) {
	for _, billed := range []bool{false, true} {
		name := "reference_free"
		if billed {
			name = "reference_billed"
		}
		t.Run(name, func(t *testing.T) {
			s, _, upstream, user := newSharedWalletTest(t)
			order := &model.BillingOrder{
				ID: "video-seconds", UserID: user.ID, IdempotencyKey: "video-seconds", Model: "video",
				Capability: "video", BillingMode: "per_second", UnitPriceMicrocredits: CreditScale,
				MultiplierBasisPoints: 10000, AmountMicrocredits: 8 * CreditScale, Quantity: 8,
				ReferenceVideoCount:  1,
				ReferenceVideoMillis: 2500, ReferenceVideoBilled: billed, Status: model.BillingStatusReserved,
			}
			order.CostBillingMode = "per_second"
			order.CostQuantity = 8
			if err := s.repo.ReserveBillingOrder(order); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkBillingRunning(order.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.taskBilling().SettleBillingWithAudioDuration(order.ID, "", 4000); err != nil {
				t.Fatal(err)
			}
			quantity := int64(4)
			if billed {
				quantity += 3
			}
			saved, err := s.repo.BillingOrder(order.ID)
			if err != nil || saved.Quantity != quantity || saved.CostQuantity != quantity || saved.ActualAmountMicrocredits != quantity*CreditScale {
				t.Fatalf("reference seconds lost from settlement: %#v, %v", saved, err)
			}
			if upstream.balance != 100-quantity {
				t.Fatalf("wallet balance = %d, want %d", upstream.balance, 100-quantity)
			}
		})
	}
}
