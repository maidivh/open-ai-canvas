package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/ecommerceagent"
	"yingce/backend/internal/model"
)

func ecommerceWalletRun(t *testing.T, s *Service, db *gorm.DB) *model.EcommerceAgentRun {
	t.Helper()
	if err := db.AutoMigrate(&model.EcommerceProject{}, &model.EcommerceAgentRun{}, &model.EcommerceAgentPage{}); err != nil {
		t.Fatal(err)
	}
	run := &model.EcommerceAgentRun{ID: "ec-run", UserID: "member", ProjectID: "ec-project", BillingOrderID: "ec-bill", IdempotencyKey: "ec-key", Status: "running", PageCount: 3, UnitMicrocredits: 1_200_000}
	project := &model.EcommerceProject{ID: run.ProjectID, UserID: run.UserID, DraftJSON: "{}", PlanJSON: "{}"}
	order := &model.BillingOrder{ID: run.BillingOrderID, UserID: run.UserID, TaskID: run.ID, IdempotencyKey: "ec-order", Scene: "ecommerce_agent", BillingMode: "fixed_request", Model: "image", Capability: "image", Quantity: 3, UnitPriceMicrocredits: 1_200_000, AmountMicrocredits: 3_600_000, ReservedAmountMicrocredits: 3_600_000, Status: model.BillingStatusReserved}
	if err := s.repo.CreateEcommerceAgentRun(run, project, order); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestEcommerceSharedWalletRunAdmission(t *testing.T) {
	s, db, up, _ := newSharedWalletTest(t)
	run := ecommerceWalletRun(t, s, db)
	if err := s.MarkBillingRunning(run.BillingOrderID); err != nil {
		t.Fatal(err)
	}
	order, _ := s.repo.BillingOrder(run.BillingOrderID)
	if order.Status != model.BillingStatusRunning || up.balance != 96 {
		t.Fatalf("order=%+v balance=%d", order, up.balance)
	}
}

func TestEcommerceSharedWalletRejectsWrongRunIdentityBeforeReserve(t *testing.T) {
	for _, field := range []string{"user_id", "billing_order_id", "cancel_requested"} {
		t.Run(field, func(t *testing.T) {
			s, db, up, _ := newSharedWalletTest(t)
			run := ecommerceWalletRun(t, s, db)
			billID := run.BillingOrderID
			var value any = "other"
			if field == "cancel_requested" {
				value = true
			}
			if err := db.Model(run).Update(field, value).Error; err != nil {
				t.Fatal(err)
			}
			// A valid generic task with the same ID must never authorize an ecommerce run.
			if err := db.Create(&model.Task{ID: run.ID, UserID: "member", BillingOrderID: billID, Status: model.TaskStatusRunning}).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.MarkBillingRunning(billID); err == nil {
				t.Fatal("wrong ecommerce identity authorized")
			}
			if up.balance != 100 {
				t.Fatalf("invalid identity precharged: %d", up.balance)
			}
		})
	}
}

func TestEcommerceSharedWalletReserveResponseLostThenFailureRefunds(t *testing.T) {
	s, db, up, _ := newSharedWalletTest(t)
	run := ecommerceWalletRun(t, s, db)
	up.failAfterCommit = true
	if err := s.MarkBillingRunning(run.BillingOrderID); err == nil {
		t.Fatal("missing response accepted")
	}
	if _, err := s.repo.FinishEcommerceAgentRun(run.UserID, run.ID, "failed", "UPSTREAM_ERROR"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.reconcileSharedWallet(context.Background()); err != nil {
		t.Fatal(err)
	}
	order, _ := s.repo.BillingOrder(run.BillingOrderID)
	if up.balance != 100 || order.Status != model.BillingStatusRefunded {
		t.Fatalf("balance=%d order=%+v", up.balance, order)
	}
}

func TestEcommerceSharedWalletFinishPersistsRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		successes    int
		balance      int64
		fail         bool
	}{
		{"success", "completed", 3, 96, false}, {"partial", "completed", 2, 97, false}, {"cancelled", "cancelled", 0, 100, false}, {"failure", "failed", 0, 100, false}, {"cancelled_partial", "cancelled", 2, 97, false}, {"failed_partial", "failed", 2, 97, false}, {"settle_response_lost", "completed", 2, 97, true}, {"refund_response_lost", "failed", 0, 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, up, _ := newSharedWalletTest(t)
			run := ecommerceWalletRun(t, s, db)
			if err := s.MarkBillingRunning(run.BillingOrderID); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < tc.successes; i++ {
				if err := s.repo.SaveEcommerceAgentPage(run.UserID, &model.EcommerceAgentPage{ID: fmt.Sprintf("page-%d", i), RunID: run.ID, Index: i, Status: "completed"}); err != nil {
					t.Fatal(err)
				}
			}
			finished, err := s.repo.FinishEcommerceAgentRun(run.UserID, run.ID, tc.status, "")
			if err != nil {
				t.Fatal(err)
			}
			if finished.Status != tc.status || finished.SuccessPages != tc.successes {
				t.Fatalf("run=%+v", finished)
			}
			if err := s.repo.SaveEcommerceAgentPage(run.UserID, &model.EcommerceAgentPage{ID: "late-page", RunID: run.ID, Index: 99, Status: "completed"}); err == nil {
				t.Fatal("late page altered frozen success count")
			}
			ops, err := s.repo.WalletOperations(run.BillingOrderID)
			if err != nil || len(ops) != 2 {
				t.Fatalf("terminal intention not atomic: ops=%+v err=%v", ops, err)
			}
			up.failAfterCommit = tc.fail
			_, _ = s.reconcileSharedWallet(context.Background())
			if _, err = s.repo.FinishEcommerceAgentRun(run.UserID, run.ID, "failed", "duplicate"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.reconcileSharedWallet(context.Background()); err != nil {
				t.Fatal(err)
			}
			order, _ := s.repo.BillingOrder(run.BillingOrderID)
			want := model.BillingStatusSettled
			if tc.successes == 0 {
				want = model.BillingStatusRefunded
			}
			if up.balance != tc.balance || order.Status != want {
				t.Fatalf("balance=%d order=%+v", up.balance, order)
			}
			ops, err = s.repo.WalletOperations(run.BillingOrderID)
			if err != nil || len(ops) != 2 {
				t.Fatalf("duplicate finish added intention: ops=%+v err=%v", ops, err)
			}
			account, _ := s.repo.CreditAccount(run.UserID)
			if account.AvailableMicrocredits != 50_000_000 || account.ReservedMicrocredits != 0 {
				t.Fatalf("internal account changed: %+v", account)
			}
		})
	}
}

func TestEcommerceSharedWalletCancellationDuringReserveRefunds(t *testing.T) {
	s, db, up, _ := newSharedWalletTest(t)
	run := ecommerceWalletRun(t, s, db)
	up.beforeReserve = func() {
		if err := s.repo.RequestEcommerceAgentCancel(run.UserID, run.ID); err != nil {
			t.Error(err)
		}
	}
	if err := s.MarkBillingRunning(run.BillingOrderID); err == nil {
		t.Fatal("cancelled run authorized after reserve")
	}
	if _, err := s.finishEcommerceAgentRun(run.UserID, run.ID, "cancelled", "CANCELLED"); err != nil {
		t.Fatal(err)
	}
	order, _ := s.repo.BillingOrder(run.BillingOrderID)
	if up.balance != 100 || order.Status != model.BillingStatusRefunded {
		t.Fatalf("balance=%d order=%+v", up.balance, order)
	}
}

func TestEcommerceSharedWalletRejectedReserveDoesNotInventRefund(t *testing.T) {
	s, db, up, _ := newSharedWalletTest(t)
	run := ecommerceWalletRun(t, s, db)
	up.balance = 0
	if err := s.MarkBillingRunning(run.BillingOrderID); err == nil {
		t.Fatal("insufficient reserve accepted")
	}
	if _, err := s.finishEcommerceAgentRun(run.UserID, run.ID, "failed", "INSUFFICIENT_CREDITS"); err != nil {
		t.Fatal(err)
	}
	order, _ := s.repo.BillingOrder(run.BillingOrderID)
	if order.Status != model.BillingStatusRefunded || order.RefundedAmountMicrocredits != 0 || up.balance != 0 {
		t.Fatalf("phantom refund: order=%+v balance=%d", order, up.balance)
	}
	var entries int64
	if err := db.Model(&model.CreditLedgerEntry{}).Where("billing_order_id = ?", order.ID).Count(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if entries != 0 {
		t.Fatalf("rejected reserve produced ledger entries: %d", entries)
	}
}

func TestEcommerceSharedWalletFinishUsesOriginalSource(t *testing.T) {
	s, db, up, _ := newSharedWalletTest(t)
	run := ecommerceWalletRun(t, s, db)
	if err := s.MarkBillingRunning(run.BillingOrderID); err != nil {
		t.Fatal(err)
	}
	// Force a settings change to exercise recovery against the admitted source.
	if err := db.Model(&model.SystemSetting{}).Where("key = ?", model.SharedWalletSettingKey).Update("value_json", `{"mode":"internal","version":2}`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.finishEcommerceAgentRun(run.UserID, run.ID, "failed", ""); err != nil {
		t.Fatal(err)
	}
	account, _ := s.repo.CreditAccount(run.UserID)
	if up.balance != 100 || account.AvailableMicrocredits != 50_000_000 {
		t.Fatalf("refund changed wallet source: external=%d local=%+v", up.balance, account)
	}
}

func TestEcommerceSharedWalletFinishRejectsOtherOwner(t *testing.T) {
	s, db, up, _ := newSharedWalletTest(t)
	run := ecommerceWalletRun(t, s, db)
	if err := s.MarkBillingRunning(run.BillingOrderID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.finishEcommerceAgentRun("another-user", run.ID, "failed", ""); err == nil {
		t.Fatal("other user closed run")
	}
	if up.balance != 96 {
		t.Fatalf("other user changed wallet: %d", up.balance)
	}
	if _, err := s.finishEcommerceAgentRun(run.UserID, run.ID, "failed", ""); err != nil {
		t.Fatal(err)
	}
}

func TestEcommerceSharedWalletGenerateAndReplay(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprintf("disconnect_%v", disconnect), func(t *testing.T) {
			s, db, up, _ := newSharedWalletTest(t)
			t.Setenv("AI_MODE", "mock")
			s.activeCancels = make(map[string]context.CancelFunc)
			if err := db.AutoMigrate(&model.EcommerceProject{}, &model.EcommerceAgentRun{}, &model.EcommerceAgentAnalysis{}, &model.EcommerceAgentPage{}, &model.EcommerceAgentCanvasVersion{}, &model.Resource{}, &model.EcommerceKnowledgeSource{}, &model.EcommerceKnowledgeEntry{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.SystemSetting{Key: featureAvailabilitySettingKey, ValueJSON: `{"ecommerceEnabled":true,"imageWorkbenchEnabled":true}`}).Error; err != nil {
				t.Fatal(err)
			}
			req := mockEcommerceRequest()
			quote, err := s.QuoteEcommerceAgent("member", req)
			if err != nil {
				t.Fatal(err)
			}
			if quote["totalMicrocredits"] != int64(1_000_000) {
				t.Errorf("quote understates rounded external charge: %+v", quote)
			}
			var runID string
			completed := 0
			_, err = s.GenerateEcommerceAgent(context.Background(), "member", req, func(e ecommerceagent.Event) error {
				if e.Event == "workflow_started" {
					runID = e.TaskID
					if e.Data.(map[string]any)["charged"] != float64(1) {
						t.Errorf("wrong reserve event: %+v", e)
					}
				}
				if e.Event == "iteration_completed" {
					completed++
					if disconnect && completed == 2 {
						return errors.New("client disconnected")
					}
				}
				if e.Event == "node_finished" {
					if credit, ok := e.Data.(map[string]any)["credit"].(map[string]any); ok && credit["charged"] != float64(1) {
						t.Errorf("wrong charged event: %+v", credit)
					}
				}
				return nil
			})
			if (!disconnect && err != nil) || (disconnect && err == nil) {
				t.Fatalf("generation error=%v", err)
			}
			view, err := s.EcommerceAgentTask("member", runID)
			if err != nil {
				t.Fatal(err)
			}
			wantPages := 5
			if disconnect {
				wantPages = 2
			}
			if view.Run.SuccessPages != wantPages || up.balance != 99 {
				t.Fatalf("run=%+v balance=%d", view.Run, up.balance)
			}
			if _, err = s.GenerateEcommerceAgent(context.Background(), "member", req, func(e ecommerceagent.Event) error {
				if e.Event == "workflow_started" && e.Data.(map[string]any)["charged"] != float64(1) {
					t.Errorf("replay understated charge: %+v", e)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if up.balance != 99 {
				t.Fatalf("replay double charged: %d", up.balance)
			}
		})
	}
}
