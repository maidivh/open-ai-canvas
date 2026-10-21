package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
	"yingce/backend/internal/repository"
)

type walletTestServer struct {
	mu              sync.Mutex
	balance         int64
	calls           map[string]int
	receipts        map[string]walletReply
	bills           map[string]walletReply
	failAfterCommit bool
	rejectSettle    bool
	beforeReserve   func()
}

func newSharedWalletTest(t *testing.T) (*Service, *gorm.DB, *walletTestServer, *model.User) {
	t.Helper()
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	upstream := &walletTestServer{balance: 100, calls: map[string]int{}, receipts: map[string]walletReply{}, bills: map[string]walletReply{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Wallet-Sign") != walletSignature(strings.Repeat("s", 32), r.URL.Path, r.Header.Get("X-Wallet-Ts"), r.Header.Get("X-Wallet-Nonce"), raw) {
			t.Error("invalid cross-wire signature")
			w.WriteHeader(401)
			return
		}
		var input struct {
			Site      int64  `json:"site_id"`
			Member    int64  `json:"member_id"`
			Trade     string `json:"trade_no"`
			Operation string `json:"operation_id"`
			Point     int64  `json:"point"`
		}
		if json.Unmarshal(raw, &input) != nil || input.Site != 7 || input.Member != 11 {
			t.Error("wrong external identity")
			w.WriteHeader(400)
			return
		}
		action := strings.TrimPrefix(r.URL.Path, "/api/ht_hot/billing/v1/")
		upstream.calls[action]++
		if action == "reserve" && upstream.beforeReserve != nil {
			upstream.beforeReserve()
		}
		reply := walletReply{OK: true, Balance: upstream.balance}
		if action == "status" {
			reply = upstream.bills[input.Trade]
			if reply.TradeNo == "" {
				reply = walletReply{OK: false, Reason: "not_found", Balance: upstream.balance}
			}
		}
		if action != "balance" && action != "status" {
			if old, ok := upstream.receipts[input.Operation]; ok {
				reply = old
			} else {
				reply.TradeNo = input.Trade
				old := upstream.bills[input.Trade]
				switch action {
				case "reserve":
					if input.Point > upstream.balance {
						reply.OK = false
						reply.Reason = "insufficient"
					} else {
						upstream.balance -= input.Point
						reply.Status = "reserved"
						reply.Reserved = input.Point
						reply.Charged = input.Point
					}
				case "settle":
					if upstream.rejectSettle {
						reply.OK = false
						reply.Reason = "invalid_state"
					} else {
						upstream.balance += old.Charged - input.Point
						reply.Status = "settled"
						reply.Reserved = old.Reserved
						reply.Charged = input.Point
					}
				case "refund":
					upstream.balance += old.Charged
					reply.Status = "refunded"
					reply.Reserved = old.Reserved
				case "restore":
					if input.Point > upstream.balance {
						reply.OK = false
						reply.Reason = "insufficient"
					} else {
						upstream.balance -= input.Point
						reply.Status = "settled"
						reply.Reserved = old.Reserved
						reply.Charged = input.Point
					}
				default:
					t.Errorf("unexpected action %s", action)
				}
				reply.Balance = upstream.balance
				if reply.OK {
					upstream.bills[input.Trade] = reply
					upstream.receipts[input.Operation] = reply
				}
			}
			if upstream.failAfterCommit {
				upstream.failAfterCommit = false
				w.WriteHeader(503)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "data": reply})
	}))
	t.Cleanup(server.Close)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	if err = db.AutoMigrate(&model.SystemSetting{}, &model.CreditAccount{}, &model.CreditLedgerEntry{}, &model.BillingOrder{}, &model.WalletOperation{}, &model.UserIdentity{}, &model.User{}, &model.AdminAuditEvent{}, &model.PaymentOrder{}, &model.Task{}, &model.ApiCallLog{}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{repo: repository.New(db), dataDir: t.TempDir()}
	cipher, err := svc.encryptSettingSecret(strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.SharedWalletSettings{Mode: "external", BaseURL: server.URL, ClientID: "test-canvas", SiteID: 7, Issuer: "https://id.example", SecretCipher: cipher, Version: 1}
	raw, _ := json.Marshal(cfg)
	if err = db.Create(&model.SystemSetting{Key: model.SharedWalletSettingKey, ValueJSON: string(raw)}).Error; err != nil {
		t.Fatal(err)
	}
	user := &model.User{ID: "member", Username: "member", Role: model.UserRoleUser, Status: model.UserStatusActive}
	if err = db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(cfg.Issuer))
	if err = db.Create(&model.UserIdentity{ID: "identity", UserID: user.ID, Provider: "cloooud", Subject: hex.EncodeToString(hash[:]) + ":7:11"}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.CreditAccount{UserID: user.ID, AvailableMicrocredits: 50000000}).Error; err != nil {
		t.Fatal(err)
	}
	return svc, db, upstream, user
}
func walletTestOrder(t *testing.T, s *Service, id string, amount int64) *model.BillingOrder {
	t.Helper()
	order := &model.BillingOrder{ID: id, UserID: "member", IdempotencyKey: id, Model: "test-model", BillingMode: "fixed_request", AmountMicrocredits: amount, Status: model.BillingStatusReserved}
	if err := s.repo.ReserveBillingOrder(order); err != nil {
		t.Fatal(err)
	}
	return order
}

func TestSharedWalletLifecycleAndOriginalSource(t *testing.T) {
	s, db, up, user := newSharedWalletTest(t)
	order := walletTestOrder(t, s, "bill-1", 1500001)
	if order.WalletState != "pending" || order.AmountMicrocredits != 2000000 {
		t.Fatalf("admission not rounded/durable: %+v", order)
	}
	if up.balance != 100 {
		t.Fatal("HTTP ran inside admission")
	}
	if err := s.MarkBillingRunning(order.ID); err != nil {
		t.Fatal(err)
	}
	if up.balance != 98 {
		t.Fatal("did not reserve external funds")
	}
	if err := s.SettleBilling(order.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleBilling(order.ID, ""); err != nil {
		t.Fatal(err)
	}
	if up.calls["settle"] != 1 {
		t.Fatal("duplicate remote settlement")
	}
	account, _ := s.repo.CreditAccount(user.ID)
	if account.AvailableMicrocredits != 50000000 {
		t.Fatal("external bill spent internal funds")
	}
	wallet, err := s.Wallet(user, "", 1, 30)
	if err != nil || wallet.WalletMode != "external" || wallet.Account.AvailableMicrocredits != 98000000 {
		t.Fatalf("wallet %+v %v", wallet, err)
	}
	order2 := walletTestOrder(t, s, "bill-2", 1000000)
	if err = s.MarkBillingRunning(order2.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RefundBilling(order2.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	if err = s.repo.SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.taskBilling().RestoreRefundedBilling(order2.ID, ""); err != nil {
		t.Fatal(err)
	}
	if up.balance != 97 {
		t.Fatalf("restore lost original wallet: %d", up.balance)
	}
	var saved model.BillingOrder
	db.First(&saved, "id = ?", order2.ID)
	if saved.WalletMode != "external" || saved.Status != model.BillingStatusSettled {
		t.Fatalf("lost source: %+v", saved)
	}
}

func TestSharedWalletTimeoutReplaysOriginalOperation(t *testing.T) {
	s, _, up, _ := newSharedWalletTest(t)
	order := walletTestOrder(t, s, "timeout", 3000000)
	up.failAfterCommit = true
	if err := s.MarkBillingRunning(order.ID); err == nil {
		t.Fatal("remote unknown treated as success")
	}
	if up.balance != 97 {
		t.Fatal("test did not simulate remote commit")
	}
	if _, err := s.reconcileSharedWallet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBillingRunning(order.ID); err != nil {
		t.Fatal(err)
	}
	if up.balance != 97 {
		t.Fatal("replay charged twice")
	}
	if up.calls["status"] == 0 {
		t.Fatal("unknown operation was not queried before replay")
	}
}

func TestSharedWalletReconciliationProgressesPastFailedOrders(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprintf("admin=%v", manual), func(t *testing.T) {
			s, db, up, _ := newSharedWalletTest(t)
			for i := 0; i < 50; i++ {
				order := walletTestOrder(t, s, fmt.Sprintf("a-stuck-%02d", i), CreditScale)
				up.failAfterCommit = true
				if err := s.MarkBillingRunning(order.ID); err == nil {
					t.Fatal("unconfirmed reserve unexpectedly succeeded")
				}
				var cfg model.SharedWalletSettings
				if err := json.Unmarshal([]byte(order.WalletConfigJSON), &cfg); err != nil {
					t.Fatal(err)
				}
				// The original credentials can become unreadable before the next
				// attempt, so fairness must not depend on reaching the remote API.
				cfg.SecretCipher = "enc:v1:unreadable-original-secret"
				raw, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&model.BillingOrder{}).Where("id = ?", order.ID).Update("wallet_config_json", string(raw)).Error; err != nil {
					t.Fatal(err)
				}
			}
			ready := walletTestOrder(t, s, "z-ready-refund", 2*CreditScale)
			if err := s.MarkBillingRunning(ready.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.repo.QueueWalletOperation(ready.ID, "refund", nil); err != nil {
				t.Fatal(err)
			}
			for round := 0; round < 3; round++ {
				var done int
				var err error
				if manual {
					done, err = s.ReconcileSharedWallet(context.Background(), &model.User{ID: "admin", Role: model.UserRoleAdmin})
				} else {
					done, err = s.reconcileSharedWallet(context.Background())
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("round %d completed %d orders", round+1, done)
				if round == 0 {
					saved, err := s.repo.BillingOrder(ready.ID)
					if err != nil || saved.Status == model.BillingStatusRefunded {
						t.Fatalf("first reconciliation exceeded its bounded batch: %+v %v", saved, err)
					}
				}
			}
			saved, err := s.repo.BillingOrder(ready.ID)
			if err != nil || saved.Status != model.BillingStatusRefunded {
				t.Fatalf("later refund starved across consecutive reconciliation rounds: %+v %v", saved, err)
			}
			if up.balance != 50 || up.calls["refund"] != 1 {
				t.Fatalf("refund did not execute exactly once: balance=%d calls=%d", up.balance, up.calls["refund"])
			}
			var pending int64
			if err := db.Model(&model.WalletOperation{}).Where("state = ?", "pending").Count(&pending).Error; err != nil {
				t.Fatal(err)
			}
			if pending != 50 {
				t.Fatalf("failed operations were discarded instead of retained: %d pending", pending)
			}
		})
	}
}

func TestSharedWalletAdminReconciliationProgressesPastRejectedRestores(t *testing.T) {
	s, _, up, _ := newSharedWalletTest(t)
	var ids []string
	for i := 0; i < 51; i++ {
		amount := int64(2 * CreditScale)
		if i == 50 {
			amount = CreditScale
		}
		order := walletTestOrder(t, s, fmt.Sprintf("restore-%02d", i), amount)
		if err := s.MarkBillingRunning(order.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.RefundBilling(order.ID, "provider failure"); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, order.ID)
	}
	up.balance = 0
	for _, id := range ids {
		if err := s.taskBilling().RestoreRefundedBilling(id, "provider-success"); err == nil {
			t.Fatal("restore unexpectedly succeeded before topup")
		}
	}
	up.balance = 1
	for round := 0; round < 3; round++ {
		done, err := s.ReconcileSharedWallet(context.Background(), &model.User{ID: "admin", Role: model.UserRoleAdmin})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("admin round %d restored %d orders", round+1, done)
		if round == 0 && done != 0 {
			t.Fatalf("first admin round exceeded its bounded restore batch: %d", done)
		}
	}
	for i, id := range ids {
		want := model.BillingStatusRefunded
		if i == 50 {
			want = model.BillingStatusSettled
		}
		saved, err := s.repo.BillingOrder(id)
		if err != nil || saved.Status != want {
			t.Fatalf("restore %s: want %s, got %+v %v", id, want, saved, err)
		}
	}
	if up.balance != 0 {
		t.Fatalf("later affordable restore did not charge exactly once: balance=%d", up.balance)
	}
}

func TestSharedWalletRejectedSettlementStaysErrorOnReplay(t *testing.T) {
	s, _, up, _ := newSharedWalletTest(t)
	order := walletTestOrder(t, s, "reject-settle", 1000000)
	if err := s.MarkBillingRunning(order.ID); err != nil {
		t.Fatal(err)
	}
	up.rejectSettle = true
	for i := 0; i < 2; i++ {
		if err := s.SettleBilling(order.ID, ""); err == nil {
			t.Fatalf("rejected settlement became successful on attempt %d", i)
		}
	}
}

func TestSharedWalletModeSwitchAndRewards(t *testing.T) {
	s, db, _, user := newSharedWalletTest(t)
	if err := s.ensureSignupBonus(user.ID); err != nil {
		t.Fatal("external signup blocked login", err)
	}
	if _, _, err := s.CheckinCredits(user); err == nil {
		t.Fatal("external checkin permitted")
	}
	if _, err := s.repo.AdjustCredits(user.ID, "admin", 1000000, "test"); err == nil {
		t.Fatal("external manual grant permitted")
	}
	if _, err := s.repo.RedeemCode(user.ID, "test", ""); err == nil {
		t.Fatal("external redeem permitted")
	}
	if _, _, err := s.repo.CreatePaymentOrderWithProductReservation(&model.PaymentOrder{}); err == nil {
		t.Fatal("external payment permitted")
	}
	feature, err := s.FeatureAvailability()
	if err != nil || !feature.CreditsEnabled || feature.WalletMode != "external" {
		t.Fatalf("features %+v %v", feature, err)
	}
	order := walletTestOrder(t, s, "blocking", 1000000)
	if err = s.repo.SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 1); err == nil {
		t.Fatal("switched with pending bill")
	}
	if err = s.RefundBilling(order.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if err = s.repo.SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.ensureSignupBonus(user.ID); err != nil {
		t.Fatal(err)
	}
	account, _ := s.repo.CreditAccount(user.ID)
	if account.AvailableMicrocredits != 50000000 {
		t.Fatal("external registration received backfill")
	}
	if _, granted, err := s.CheckinCredits(user); err != nil || !granted {
		t.Fatalf("internal checkin lost: %v %v", granted, err)
	}
	if err = s.repo.SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 1); err == nil {
		t.Fatal("stale version overwrote config")
	}
	var count int64
	db.Model(&model.CreditLedgerEntry{}).Where("reference_key = ?", fmt.Sprintf("signup:%s", user.ID)).Count(&count)
	if count != 1 {
		t.Fatal("missing no-backfill marker")
	}
}

func TestSharedWalletRestoredOrderCannotReplayOldRefund(t *testing.T) {
	s, _, up, _ := newSharedWalletTest(t)
	o := walletTestOrder(t, s, "restore-refund", 1000000)
	if err := s.MarkBillingRunning(o.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RefundBilling(o.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	if err := s.taskBilling().RestoreRefundedBilling(o.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RefundBilling(o.ID, "again"); err == nil {
		t.Fatal("old refund receipt falsely approved refund after restore")
	}
	if up.balance != 99 {
		t.Fatal("unexpected remote balance")
	}
}

func TestSharedWalletRejectedReserveDoesNotInventRefund(t *testing.T) {
	s, _, up, _ := newSharedWalletTest(t)
	o := walletTestOrder(t, s, "insufficient", 101000000)
	err := s.MarkBillingRunning(o.ID)
	if err == nil {
		t.Fatal("insufficient funds admitted")
	}
	var rejected *walletBusinessError
	if !errors.As(err, &rejected) || rejected.Reason != "insufficient" || !strings.Contains(err.Error(), "外部钱包") || strings.Contains(err.Error(), "insufficient") {
		t.Fatalf("external rejection lost its machine reason or user-facing wallet source: %v", err)
	}
	var public *AppError
	if !errors.As(err, &public) || public.Status != 403 || public.Code != CodeQuotaExceeded || public.Reason != ReasonQuotaExceeded || public.Message != err.Error() {
		t.Fatalf("synchronous proxy would hide the rejected balance as an internal error: %v", err)
	}
	if err := s.RefundBilling(o.ID, "preparation failed"); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.repo.BillingOrder(o.ID)
	if saved.RefundedAmountMicrocredits != 0 {
		t.Fatal("refund claimed for funds never reserved")
	}
	if up.balance != 100 || up.calls["refund"] != 0 {
		t.Fatal("rejected reserve called remote refund")
	}
}

func TestSharedWalletOverrunRemainsUncertain(t *testing.T) {
	s, db, _, _ := newSharedWalletTest(t)
	o := walletTestOrder(t, s, "overrun", 1000000)
	if err := s.MarkBillingRunning(o.ID); err != nil {
		t.Fatal(err)
	}
	db.Model(&model.BillingOrder{}).Where("id = ?", o.ID).Update("amount_microcredits", 2000000)
	if err := s.SettleBilling(o.ID, ""); err == nil {
		t.Fatal("overrun charged without approval")
	}
	saved, _ := s.repo.BillingOrder(o.ID)
	if saved.Status != model.BillingStatusUncertain {
		t.Fatalf("overrun hidden as %s", saved.Status)
	}
}

func TestSharedWalletRestoreCanRetryAfterTopup(t *testing.T) {
	s, _, up, _ := newSharedWalletTest(t)
	o := walletTestOrder(t, s, "restore-topup", 2000000)
	if err := s.MarkBillingRunning(o.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RefundBilling(o.ID, "failure"); err != nil {
		t.Fatal(err)
	}
	up.balance = 0
	if err := s.taskBilling().RestoreRefundedBilling(o.ID, ""); err == nil {
		t.Fatal("restore should lack funds")
	}
	saved, _ := s.repo.BillingOrder(o.ID)
	if saved.Status != model.BillingStatusRefunded {
		t.Fatal("definite restore rejection lost refunded state")
	}
	up.balance = 10
	if err := s.taskBilling().MarkBillingUncertain(o.ID, "recovery caller wrapping error"); err != nil {
		t.Fatal(err)
	}
	saved, _ = s.repo.BillingOrder(o.ID)
	if saved.Status != model.BillingStatusRefunded {
		t.Fatal("recovery wrapper destroyed definite refunded state")
	}
	if _, err := s.ReconcileSharedWallet(context.Background(), &model.User{ID: "admin", Role: model.UserRoleAdmin}); err != nil {
		t.Fatal(err)
	}
	if up.balance != 8 {
		t.Fatal("restore did not charge once after topup")
	}
}

func TestSharedWalletUnboundedAudioRejected(t *testing.T) {
	if _, err := prepareWalletQuote(&model.BillingOrder{BillingMode: "per_second", Capability: "audio", AmountMicrocredits: 1000000}, model.SharedWalletSettings{Mode: "external"}); err == nil {
		t.Fatal("unbounded audio estimate admitted as an authorization")
	}
}

func TestSharedWalletRouteSwitchPreservesExternalReservation(t *testing.T) {
	s, db, up, _ := newSharedWalletTest(t)
	o := walletTestOrder(t, s, "route-switch", 3000000)
	if err := s.MarkBillingRunning(o.ID); err != nil {
		t.Fatal(err)
	}
	db.Model(&model.BillingOrder{}).Where("id = ?", o.ID).Update("task_id", "route-task")
	db.Create(&model.Task{ID: "route-task", UserID: "member", Status: model.TaskStatusRunning, RouteID: "old"})
	replacement := &model.BillingOrder{UserID: "member", TaskID: "route-task", BillingMode: "fixed_request", AmountMicrocredits: 2000000}
	if err := s.repo.SwitchTaskLogicalRoute("route-task", "old", "new", "{}", o.ID, "channel", "model", replacement, model.BillingCostSnapshot{}); err != nil {
		t.Fatal(err)
	}
	account, _ := s.repo.CreditAccount("member")
	if account.AvailableMicrocredits != 50000000 || account.ReservedMicrocredits != 0 {
		t.Fatal("external fallback mutated local wallet")
	}
	saved, _ := s.repo.BillingOrder(o.ID)
	if saved.ReservedAmountMicrocredits != 3000000 {
		t.Fatal("original remote authorization overwritten")
	}
	if err := s.SettleBilling(o.ID, ""); err != nil {
		t.Fatal(err)
	}
	if up.balance != 98 {
		t.Fatal("cheaper route did not settle against original authorization")
	}
}

func TestSharedWalletTextAuthorizationUsesProviderBounds(t *testing.T) {
	wallet := model.SharedWalletSettings{Mode: "external"}
	item := &model.ChannelModel{Capability: "text", Protocol: "chat-completion", CapabilityConfigJSON: `{"text":{"contextWindowTokens":128000,"maxOutputTokens":16384}}`}
	estimate, err := walletTokenEstimate(estimateTaskTokens(map[string]any{"prompt": "中文测试"}), item, "token", wallet)
	if err != nil || estimate.InputTokens != 128000 || estimate.OutputTokens != 16384 || !estimate.ExternalBudget {
		t.Fatalf("unsafe token authorization %+v %v", estimate, err)
	}
	bounded := estimateProxyTokens([]byte(`{"max_tokens":200}`))
	if result, e := walletTokenEstimate(bounded, item, "token", wallet); e != nil || result.OutputTokens != 200 {
		t.Fatalf("bounded proxy rejected %+v %v", result, e)
	}
	for _, body := range []string{`{}`, `{"maxOutputTokens":200}`, `{"max_output_tokens":200}`, `{"max_tokens":200,"max_completion_tokens":9000}`, `{"max_tokens":200,"n":10}`, `{"max_tokens":200,"n":0}`, `{"max_tokens":200,"n":1.5}`} {
		if _, e := walletTokenEstimate(estimateProxyTokens([]byte(body)), item, "token", wallet); e == nil {
			t.Fatalf("unsafe wire budget accepted %s", body)
		}
	}
	agentEstimate := estimateTaskTokens(map[string]any{"agentRequests": map[string]any{"chatCompletion": map[string]any{"n": 10, "max_tokens": 200}}})
	if _, e := walletTokenEstimate(agentEstimate, item, "token", wallet); e == nil {
		t.Fatal("multiple-output Agent request admitted")
	}
	item.Protocol = "custom-unbounded"
	if _, e := walletTokenEstimate(bounded, item, "token", wallet); e == nil {
		t.Fatal("unproven adapter admitted")
	}
	item.Protocol = "chat-completion"
	item.CapabilityConfigJSON = ""
	if _, e := walletTokenEstimate(bounded, item, "token", wallet); e == nil {
		t.Fatal("undeclared context ceiling admitted")
	}
}

func TestSharedWalletAgentBodyUsesSameFrozenLimit(t *testing.T) {
	input := canvasGenerationInput{MaxOutputTokens: 200, EnforceWalletOutputLimit: true, TextOptions: canvasTextOptions{MaxOutputTokens: 100}}
	for _, wire := range []string{"chat-completion", "responses", "claude-api"} {
		body := map[string]any{"n": 1, "max_tokens": 9000, "max_output_tokens": 8000, "max_completion_tokens": 7000}
		if err := enforceWalletTextBody(body, input, wire); err != nil {
			t.Fatal(err)
		}
		expected := "max_tokens"
		if wire == "responses" {
			expected = "max_output_tokens"
		}
		if body[expected] != 100 || len(body) != 2 {
			t.Fatalf("conflicting Agent limits left: %+v", body)
		}
		body["n"] = 10
		if err := enforceWalletTextBody(body, input, wire); err == nil {
			t.Fatal("multi-output Agent escaped final guard")
		}
	}
}

func TestSharedWalletAppliesFrozenLimitAfterProtocolMapping(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["max_tokens"] != float64(200) || body["max_output_tokens"] != nil || body["max_completion_tokens"] != nil {
			t.Errorf("wire limit not frozen: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	input := canvasGenerationInput{Mode: "text", Config: providerConfig{BaseURL: server.URL, InterfaceType: "chat-completion"}, MaxOutputTokens: 200, EnforceWalletOutputLimit: true}
	_, _, err := executeProtocolCreateRequest(context.Background(), input, protocol.RequestSpec{Method: "POST", Path: "/test", ContentType: "application/json", Body: map[string]any{"max_tokens": 9999, "max_output_tokens": 9999, "max_completion_tokens": 9999}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSharedWalletSettingsPreserveSecretAndCAS(t *testing.T) {
	s, _, _, user := newSharedWalletTest(t)
	if _, err := s.AdminWalletSettings(user); err == nil {
		t.Fatal("ordinary user can read wallet settings")
	}
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	view, err := s.AdminWalletSettings(admin)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "secretCipher") || strings.Contains(string(raw), strings.Repeat("s", 32)) {
		t.Fatal("secret leaked")
	}
	original, _ := s.repo.SharedWalletSettings()
	view.RechargeURL = "https://id.example/recharge"
	saved, err := s.UpdateWalletSettings(context.Background(), admin, WalletSettingsRequest{WalletSettingsView: *view})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 2 || !saved.SecretConfigured {
		t.Fatal("save lost version or secret marker")
	}
	cfg, _ := s.repo.SharedWalletSettings()
	if cfg.SecretCipher != original.SecretCipher {
		t.Fatal("blank secret replaced encrypted value")
	}
	if _, err = s.UpdateWalletSettings(context.Background(), admin, WalletSettingsRequest{WalletSettingsView: *view}); err == nil {
		t.Fatal("stale config overwrite accepted")
	}
}

func TestSharedWalletFreeProxyStillBlocksModeSwitch(t *testing.T) {
	s, db, _, _ := newSharedWalletTest(t)
	if err := s.repo.SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 1); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.SystemSetting{Key: "feature_availability", ValueJSON: `{"creditsEnabled":false}`})
	order, err := s.ReserveProxyBilling("member", "channel", "model", "text", "test", "free-proxy", 1)
	if err != nil || order == nil || order.AmountMicrocredits != 0 {
		t.Fatalf("free proxy missing admission: %+v %v", order, err)
	}
	if err = s.repo.SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 2); err == nil {
		t.Fatal("switched while free proxy in flight")
	}
	if err = s.SettleBilling(order.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.repo.SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 2); err != nil {
		t.Fatal(err)
	}
}

func TestSharedWalletFreeProxyAllowsRecoveryDebtAndPreservesAdmission(t *testing.T) {
	s, db, _, _ := newSharedWalletTest(t)
	if err := s.repo.SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SystemSetting{Key: "feature_availability", ValueJSON: `{"creditsEnabled":false}`}).Error; err != nil {
		t.Fatal(err)
	}
	old := &model.BillingOrder{ID: "old-refunded", UserID: "member", IdempotencyKey: "old-refunded", BillingMode: "fixed_request", AmountMicrocredits: 51 * CreditScale, Status: model.BillingStatusRefunded}
	if err := db.Create(old).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.RestoreRefundedBillingOrder(old.ID, "provider-success"); err != nil {
		t.Fatal(err)
	}
	account, err := s.repo.CreditAccount("member")
	if err != nil || account.AvailableMicrocredits != -CreditScale {
		t.Fatalf("recovery did not create expected debt: %+v %v", account, err)
	}

	order, err := s.ReserveProxyBilling("member", "channel", "model", "text", "test", "free-with-debt", 1)
	if err != nil || order == nil {
		t.Fatalf("free proxy rejected with credits disabled: %+v %v", order, err)
	}
	saved, err := s.repo.BillingOrder(order.ID)
	if err != nil || saved.AmountMicrocredits != 0 || saved.Status != model.BillingStatusReserved || saved.WalletMode != "internal" {
		t.Fatalf("free proxy lost durable admission: %+v %v", saved, err)
	}
	if err := s.repo.SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 2); err == nil {
		t.Fatal("mode switch bypassed the free proxy in flight")
	}
	if err := s.MarkBillingRunning(order.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleBilling(order.ID, "free-success"); err != nil {
		t.Fatal(err)
	}
	account, err = s.repo.CreditAccount("member")
	if err != nil || account.AvailableMicrocredits != -CreditScale || account.ReservedMicrocredits != 0 {
		t.Fatalf("free proxy changed recovery debt: %+v %v", account, err)
	}
	paid := &model.BillingOrder{ID: "paid-with-debt", UserID: "member", IdempotencyKey: "paid-with-debt", BillingMode: "fixed_request", AmountMicrocredits: CreditScale, Status: model.BillingStatusReserved}
	if err := s.repo.ReserveBillingOrder(paid); err != repository.ErrInsufficientCredits {
		t.Fatalf("paid request bypassed insufficient funds: %v", err)
	}
	if err := s.repo.SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 2); err != nil {
		t.Fatal(err)
	}
}

func TestSharedWalletUnbilledSystemTaskCannotSlipAcrossModeSwitch(t *testing.T) {
	s, _, _, _ := newSharedWalletTest(t)
	task := &model.Task{ID: "stale-free", UserID: "member", Status: model.TaskStatusQueued, InputJSON: `{"config":{"channelId":"system-paid"}}`}
	if err := s.repo.CreateTaskWithActiveLimit(task, 10); err == nil {
		t.Fatal("stale free admission bypassed external billing")
	}
}

func TestSharedWalletExternalQuoteRoundsBeforeApproval(t *testing.T) {
	s, db, channel, channelModel, _ := newVideoTokenQuoteFixture(t)
	cfg := model.SharedWalletSettings{Mode: "external", Version: 1}
	raw, _ := json.Marshal(cfg)
	if err := db.Create(&model.SystemSetting{Key: model.SharedWalletSettingKey, ValueJSON: string(raw)}).Error; err != nil {
		t.Fatal(err)
	}
	intent := ModelRequestIntent{Capability: "video", Options: map[string]any{"duration": 5, "resolution": "720p", "aspectRatio": "16:9"}}
	quote, err := s.QuoteChannelModel(ChannelModelQuoteRequest{ChannelID: channel.ID, ModelKey: channelModel.ModelKey, Intent: intent})
	if err != nil {
		t.Fatal(err)
	}
	if quote.AmountMicrocredits%CreditScale != 0 {
		t.Fatalf("unrounded external quote: %d", quote.AmountMicrocredits)
	}
}
