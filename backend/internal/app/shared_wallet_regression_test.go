package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

func TestSharedWalletQuoteKeepsOriginalConfigurationUntilAdmission(t *testing.T) {
	for _, entry := range []string{"price-tier", "legacy", "logical-unified", "logical-channel"} {
		for _, transition := range [][2]string{{"internal", "external"}, {"external", "internal"}, {"external", "external"}} {
			t.Run(entry+"/"+transition[0]+"-to-"+transition[1], func(t *testing.T) {
				s, db, _, _ := newSharedWalletTest(t)
				item, task := newWalletQuoteRegressionModels(t, s, db, entry)
				cfg := setWalletRegressionMode(t, s, transition[0])
				switched := switchWalletAfterSnapshotRead(t, s, db, cfg, transition[1])
				input := map[string]any{"mode": "text", "prompt": "hi", "textOptions": map[string]any{"maxOutputTokens": 200}, "config": map[string]any{}}
				var order *model.BillingOrder
				var err error
				switch entry {
				case "price-tier":
					order, err = s.newBillingOrderWithPriceTier("member", "", "race-request", item.ChannelID, item.ModelKey, "text", "test", 1, estimateTaskTokens(input), "race-tier")
				case "legacy":
					order, err = s.newBillingOrder("member", "", "race-request", item.ChannelID, item.ModelKey, "text", "test", 1, estimateTaskTokens(input))
				default:
					order, err = s.newLogicalModelBillingOrder("member", task, input)
				}
				if err != nil {
					t.Fatal(err)
				}
				if !*switched {
					t.Fatal("configuration interleaving was not reached")
				}
				if order.WalletMode != cfg.Mode || order.WalletVersion != cfg.Version {
					t.Errorf("quote replaced its original wallet snapshot: got %s/%d, want %s/%d", order.WalletMode, order.WalletVersion, cfg.Mode, cfg.Version)
				}
				if cfg.Mode == "external" && (order.AmountMicrocredits != 2_000_000 || order.Quantity != 128200 || !order.ChargeLimitSet) {
					t.Errorf("external quote lost its context ceiling or point rounding: %+v", order)
				}
				if err := s.repo.ReserveBillingOrder(order); err == nil {
					t.Fatal("configuration changed during quote but admission accepted stale authorization")
				}
				var count int64
				if err := db.Model(&model.BillingOrder{}).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("stale admission persisted an order: count=%d err=%v", count, err)
				}
			})
		}
	}
}

func TestSharedWalletModeChangeCannotBypassTokenRestrictions(t *testing.T) {
	for _, invalid := range []string{"multiple-outputs", "unsupported-protocol"} {
		t.Run(invalid, func(t *testing.T) {
			s, db, _, _ := newSharedWalletTest(t)
			item, _ := newWalletQuoteRegressionModels(t, s, db, "price-tier")
			body := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":200}`
			if invalid == "multiple-outputs" {
				body = `{"messages":[{"role":"user","content":"hi"}],"max_tokens":200,"n":2}`
			} else if err := db.Model(&item).Update("protocol", "custom-unbounded").Error; err != nil {
				t.Fatal(err)
			}
			cfg := setWalletRegressionMode(t, s, "internal")
			switchWalletAfterSnapshotRead(t, s, db, cfg, "external")
			quote := func() (*model.BillingOrder, error) {
				return s.newBillingOrderWithPriceTier("member", "", "invalid-request", item.ChannelID, item.ModelKey, "text", "test", 1, estimateProxyTokens([]byte(body)), "race-tier")
			}
			order, err := quote()
			if err != nil {
				t.Fatal(err)
			}
			if err := s.repo.ReserveBillingOrder(order); err == nil {
				t.Fatal("internal quote bypassed external token restrictions after mode change")
			}
			if _, err := quote(); err == nil {
				t.Fatal("fresh external quote accepted an unsafe token request")
			}
		})
	}
}

func TestSharedWalletLogicalQuoteUsesOneWalletSnapshot(t *testing.T) {
	s, db, _, _ := newSharedWalletTest(t)
	_, task := newWalletQuoteRegressionModels(t, s, db, "logical-unified")
	cfg := setWalletRegressionMode(t, s, "external")
	switched := switchWalletAfterSnapshotRead(t, s, db, cfg, "internal")
	quote, err := s.QuoteLogicalModel(task.LogicalModelID, ModelRequestIntent{Capability: "text"})
	if err != nil {
		t.Fatal(err)
	}
	if !*switched || quote.AmountMicrocredits != 2_000_000 || quote.Quantity != 128200 {
		t.Fatalf("logical quote mixed wallet snapshots: switched=%v quote=%+v", *switched, quote)
	}
}

func TestSharedWalletFeatureEditPreservesInternalCreditsPolicy(t *testing.T) {
	for _, original := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[original], func(t *testing.T) {
			s, _, _, _ := newSharedWalletTest(t)
			actor := &model.User{ID: "admin", Role: model.UserRoleAdmin}
			setWalletRegressionMode(t, s, "internal")
			if _, err := s.UpdateFeatureAvailability(actor, FeatureAvailability{WelcomeEnabled: true, CreditsEnabled: original}); err != nil {
				t.Fatal(err)
			}
			setWalletRegressionMode(t, s, "external")
			admin, err := s.AdminFeatureAvailability(actor)
			if err != nil {
				t.Fatal(err)
			}
			if !admin.CreditsEnabled || admin.WalletMode != "external" {
				t.Errorf("admin view lost effective credits or wallet mode: %+v", admin)
			}
			public, err := s.FeatureAvailability()
			if err != nil || !public.CreditsEnabled || public.WalletMode != "external" {
				t.Fatalf("external effective credits disabled: %+v %v", public, err)
			}
			// Admin clients echo the effective flags in their full payload.
			admin.WelcomeEnabled = false
			updated, err := s.UpdateFeatureAvailability(actor, admin.FeatureAvailability)
			if err != nil {
				t.Fatal(err)
			}
			if !updated.CreditsEnabled || updated.WalletMode != "external" || updated.WelcomeEnabled {
				t.Fatalf("admin save response lost effective feature state: %+v", updated)
			}
			setting, err := s.repo.SystemSetting(featureAvailabilitySettingKey)
			if err != nil {
				t.Fatal(err)
			}
			var persisted FeatureAvailability
			if err := json.Unmarshal([]byte(setting.ValueJSON), &persisted); err != nil {
				t.Fatal(err)
			}
			if persisted.CreditsEnabled != original || persisted.WelcomeEnabled || persisted.WalletMode != "" || persisted.RechargeURL != "" {
				t.Errorf("effective state overwrote persisted feature settings: %+v", persisted)
			}
			setWalletRegressionMode(t, s, "internal")
			current, err := s.FeatureAvailability()
			if err != nil || current.CreditsEnabled != original || current.WelcomeEnabled {
				t.Fatalf("internal policy changed after external edit: %+v %v", current, err)
			}
			for _, enabled := range []bool{!original, original} {
				current.CreditsEnabled = enabled
				if _, err := s.UpdateFeatureAvailability(actor, current.FeatureAvailability); err != nil {
					t.Fatal(err)
				}
				if got, err := s.FeatureEnabled(FeatureCredits); err != nil || got != enabled {
					t.Fatalf("legitimate internal toggle ignored: got=%v want=%v err=%v", got, enabled, err)
				}
			}
		})
	}
}

func TestSharedWalletStaleAdminFeatureEditKeepsConfiguredCredits(t *testing.T) {
	for _, original := range []bool{false, true} {
		for _, transition := range [][2]string{{"external", "internal"}, {"internal", "external"}} {
			t.Run(map[bool]string{false: "disabled", true: "enabled"}[original]+"/"+transition[0], func(t *testing.T) {
				s, _, _, _ := newSharedWalletTest(t)
				actor := &model.User{ID: "admin", Role: model.UserRoleAdmin}
				setWalletRegressionMode(t, s, "internal")
				if _, err := s.UpdateFeatureAvailability(actor, FeatureAvailability{CreditsEnabled: original, TaskCenterEnabled: true}); err != nil {
					t.Fatal(err)
				}
				setWalletRegressionMode(t, s, transition[0])
				view, err := s.AdminFeatureAvailability(actor)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(view)
				if err != nil {
					t.Fatal(err)
				}
				var response struct {
					ConfiguredFeatures *FeatureAvailability `json:"configuredFeatures"`
				}
				if err := json.Unmarshal(raw, &response); err != nil {
					t.Fatal(err)
				}
				payload := view.FeatureAvailability
				if response.ConfiguredFeatures == nil {
					t.Error("admin response omitted the persistent settings needed to edit safely")
				} else {
					payload = *response.ConfiguredFeatures
				}
				setWalletRegressionMode(t, s, transition[1])
				payload.WalletMode, payload.RechargeURL = "", ""
				payload.TaskCenterEnabled = false
				if _, err := s.UpdateFeatureAvailability(actor, payload); err != nil {
					t.Fatal(err)
				}
				setWalletRegressionMode(t, s, "internal")
				current, err := s.FeatureAvailability()
				if err != nil || current.CreditsEnabled != original || current.TaskCenterEnabled {
					t.Fatalf("stale admin form overwrote internal billing policy: %+v %v", current, err)
				}
			})
		}
	}
}

func newWalletQuoteRegressionModels(t *testing.T, s *Service, db *gorm.DB, entry string) (model.ChannelModel, *model.Task) {
	t.Helper()
	if err := db.AutoMigrate(&model.ChannelModel{}, &model.ChannelModelPriceTier{}, &model.LogicalModel{}, &model.LogicalModelRevision{}, &model.LogicalModelRoute{}); err != nil {
		t.Fatal(err)
	}
	item := model.ChannelModel{ID: "race-model", ChannelID: "race-channel", ModelKey: "race-model", Capability: "text", Protocol: "chat-completion", Enabled: true, PriceConfigured: true, BillingMode: "token", InputTokenPriceMicrocredits: 10_000_000, OutputTokenPriceMicrocredits: 10_000_000, CapabilityConfigJSON: `{"text":{"contextWindowTokens":128000,"maxOutputTokens":200}}`}
	tier := model.ChannelModelPriceTier{ID: "race-tier", ChannelModelID: item.ID, SelectorKey: "{}", SelectorJSON: "{}", Enabled: true, PriceConfigured: true, BillingMode: "token", InputTokenPriceMicrocredits: 10_000_000, OutputTokenPriceMicrocredits: 10_000_000}
	logical := model.LogicalModel{ID: "race-logical", Code: "race-logical", Capability: "text", Enabled: true, ActiveRevisionID: "race-revision", PricePolicy: "unified", BillingMode: "token", InputPriceMicrocredits: 10_000_000, OutputPriceMicrocredits: 10_000_000}
	if entry == "logical-channel" {
		logical.PricePolicy = "channel"
	}
	revision := model.LogicalModelRevision{ID: logical.ActiveRevisionID, LogicalModelID: logical.ID, Version: 1}
	route := model.LogicalModelRoute{ID: "race-route", LogicalModelRevisionID: revision.ID, ChannelModelID: item.ID, Enabled: true, Weight: 1}
	for _, row := range []any{&item, &tier, &logical, &revision, &route} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	item.PriceTiers = []model.ChannelModelPriceTier{tier}
	spec := CapabilitySpec{Version: 1, Capability: "text"}
	s.routeCatalogTTL = time.Hour
	s.routeCatalog = &routeCatalogSnapshot{LoadedAt: time.Now(), Models: map[string]cachedLogicalModel{logical.ID: {
		Model: logical, ProductSpec: spec, Routes: []cachedLogicalRoute{{Route: route, CapabilitySpec: spec, ChannelModel: item}},
	}}}
	return item, &model.Task{ID: "race-task", Type: "canvas_text", LogicalModelID: logical.ID, LogicalModelRevisionID: revision.ID, RouteID: route.ID, ChannelModelID: item.ID}
}

func setWalletRegressionMode(t *testing.T, s *Service, mode string) model.SharedWalletSettings {
	t.Helper()
	cfg, err := s.repo.SharedWalletSettings()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Mode = mode
	if err := s.repo.SaveSharedWalletSettings(cfg, cfg.Version); err != nil {
		t.Fatal(err)
	}
	cfg.Version++
	return cfg
}

func switchWalletAfterSnapshotRead(t *testing.T, s *Service, db *gorm.DB, cfg model.SharedWalletSettings, nextMode string) *bool {
	t.Helper()
	switched := false
	const callback = "wallet-regression:switch-after-snapshot"
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if switched || !strings.Contains(tx.Statement.SQL.String(), "system_settings") {
			return
		}
		for _, value := range tx.Statement.Vars {
			if value == model.SharedWalletSettingKey {
				switched = true
				cfg.Mode = nextMode
				if err := s.repo.SaveSharedWalletSettings(cfg, cfg.Version); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Query().Remove(callback) })
	return &switched
}
