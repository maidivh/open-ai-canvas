package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// Cancellation must remain authoritative when a reserve receipt arrives late.
// The real worker, cancellation service, wallet HTTP, and ledger all participate;
// only the model execution boundary is replaced to detect an unintended dispatch.
func TestSharedWalletCancelledReserveCannotDispatchOrLoseReview(t *testing.T) {
	for _, boundary := range []string{"before_request", "during_request", "after_receipt"} {
		t.Run(boundary, func(t *testing.T) {
			s, db, upstream, task := walletCancellationFixture(t)
			cancelled := false
			cancelTask := func() {
				if cancelled {
					return
				}
				cancelled = true
				if _, err := s.taskLifecycle().cancelTaskWithIntent(context.Background(), task.UserID, task.ID, model.TaskCancellationIntent{Source: model.TaskCancellationParentFailed}); err != nil {
					t.Error(err)
				}
			}
			if boundary == "during_request" {
				upstream.beforeReserve = cancelTask
			} else {
				const hook = "wallet-test:cancel-reserve"
				if err := db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(hook, func(tx *gorm.DB) {
					if cancelled {
						return
					}
					values, ok := tx.Statement.Dest.(map[string]any)
					if !ok {
						return
					}
					beforeRequest := boundary == "before_request" && tx.Statement.Table == "wallet_operations" && values["attempts"] != nil
					afterReceipt := boundary == "after_receipt" && tx.Statement.Table == "billing_orders" && values["wallet_lock_token"] == ""
					if beforeRequest || afterReceipt {
						cancelTask()
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Callback().Update().Remove(hook) })
			}
			port := &taskRouteExecutionPortStub{processResults: map[string]taskRouteExecutionStubResult{"route": {err: context.Canceled}}}
			s.taskRouteExecutor = &taskRouteExecutor{port: port}
			_ = newTaskWorkerCoordinator(s).processClaimedTask(task, nil)
			if !cancelled {
				t.Fatal("cancellation boundary was not exercised")
			}
			if len(port.processCalls) != 0 {
				t.Errorf("cancelled task entered model execution: %v", port.processCalls)
			}
			assertCancelledWalletReview(t, s, task.BillingOrderID)
			if upstream.balance != 98 {
				t.Fatalf("reserve should be recorded once pending review: %d", upstream.balance)
			}
			if _, err := s.CancelTask(context.Background(), task.UserID, task.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.processWalletOrder(context.Background(), task.BillingOrderID); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkBillingRunning(task.BillingOrderID); err == nil {
				t.Error("replayed reserve restarted a cancelled task")
			}
			assertCancelledWalletReview(t, s, task.BillingOrderID)
			for range 2 {
				if err := s.RefundBilling(task.BillingOrderID, "cancelled before provider dispatch"); err != nil {
					t.Fatal(err)
				}
			}
			if upstream.balance != 100 || upstream.calls["reserve"] != 1 || upstream.calls["refund"] != 1 {
				t.Fatalf("review/refund replay changed funds more than once: balance=%d calls=%v", upstream.balance, upstream.calls)
			}
		})
	}
}

func TestSharedWalletCancelledUnknownReserveRemainsRecoverable(t *testing.T) {
	s, _, upstream, task := walletCancellationFixture(t)
	upstream.failAfterCommit = true
	upstream.beforeReserve = func() {
		if _, err := s.taskLifecycle().cancelTaskWithIntent(context.Background(), task.UserID, task.ID, model.TaskCancellationIntent{Source: model.TaskCancellationParentCancelled}); err != nil {
			t.Error(err)
		}
	}
	if err := s.MarkBillingRunning(task.BillingOrderID); err == nil {
		t.Fatal("unknown reserve response was treated as confirmed")
	}
	upstream.beforeReserve = nil
	assertCancelledWalletReview(t, s, task.BillingOrderID)
	if upstream.balance != 98 || upstream.calls["refund"] != 0 {
		t.Fatal("unknown reserve was prematurely refunded")
	}
	if _, err := s.reconcileSharedWallet(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCancelledWalletReview(t, s, task.BillingOrderID)
	if err := s.MarkBillingRunning(task.BillingOrderID); err == nil {
		t.Fatal("reconciliation enabled a cancelled task")
	}
	if upstream.balance != 98 || upstream.calls["refund"] != 0 {
		t.Fatal("reconciliation duplicated funds or inferred a provider refund")
	}
}

func TestSharedWalletRejectedReserveStillAllowsNewAttempt(t *testing.T) {
	s, _, upstream, task := walletCancellationFixture(t)
	upstream.balance = 0
	if err := s.MarkBillingRunning(task.BillingOrderID); err == nil {
		t.Fatal("insufficient reserve succeeded")
	}
	order, err := s.repo.BillingOrder(task.BillingOrderID)
	if err != nil || order.Status != model.BillingStatusRefunded || order.WalletState != "rejected" {
		t.Fatalf("definite rejection lost its retryable state: %+v %v", order, err)
	}
	if err := s.taskBilling().CheckRetryEligibility(task.BillingOrderID); err != nil {
		t.Fatal(err)
	}
	if upstream.balance != 0 {
		t.Fatal("rejected reserve changed balance")
	}
}

func TestSharedWalletActiveTaskResumesAfterReserveReconciliation(t *testing.T) {
	s, _, upstream, task := walletCancellationFixture(t)
	upstream.failAfterCommit = true
	if err := s.MarkBillingRunning(task.BillingOrderID); err == nil {
		t.Fatal("unknown reserve response was treated as confirmed")
	}
	if _, err := s.reconcileSharedWallet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBillingRunning(task.BillingOrderID); err != nil {
		t.Fatal(err)
	}
	order, err := s.repo.BillingOrder(task.BillingOrderID)
	if err != nil || order.Status != model.BillingStatusRunning || upstream.balance != 98 {
		t.Fatalf("active task could not resume after one confirmed charge: %+v %v balance=%d", order, err, upstream.balance)
	}
}

func TestSharedWalletCancelledTaskGuardPersistsBeforeBillingMarker(t *testing.T) {
	s, _, _, task := walletCancellationFixture(t)
	if err := s.processWalletOrder(context.Background(), task.BillingOrderID); err != nil {
		t.Fatal(err)
	}
	// Cancellation commits its task write before recording billing uncertainty.
	// Admission must honor that durable stop even if the cancelling process dies.
	if ok, err := s.repo.CancelTaskIfStatus(task.UserID, task.ID, model.TaskStatusRunning, time.Now()); err != nil || !ok {
		t.Fatalf("cancel task: %v %v", ok, err)
	}
	if err := s.MarkBillingRunning(task.BillingOrderID); err == nil {
		t.Fatal("task cancellation was ignored before its billing marker")
	}
	assertCancelledWalletReview(t, s, task.BillingOrderID)
}

func TestSharedWalletInternalCancellationKeepsExistingAccounting(t *testing.T) {
	s, db, upstream, _ := newSharedWalletTest(t)
	if err := s.repo.SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.TaskLog{}, &model.TaskTextDelta{}); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{ID: "internal-cancel", UserID: "member", Status: model.TaskStatusRunning, Stage: "调用生成模型"}
	order := walletTestOrder(t, s, "internal-cancel-bill", 2000000)
	task.BillingOrderID = order.ID
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.taskLifecycle().cancelTaskWithIntent(context.Background(), task.UserID, task.ID, model.TaskCancellationIntent{Source: model.TaskCancellationParentFailed}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBillingRunning(order.ID); err == nil {
		t.Fatal("internal uncertain order restarted")
	}
	assertCancelledWalletReview(t, s, order.ID)
	account, err := s.repo.CreditAccount(task.UserID)
	if err != nil || account.AvailableMicrocredits != 48000000 || account.ReservedMicrocredits != 2000000 || len(upstream.calls) != 0 {
		t.Fatalf("internal reservation changed wallet source: %+v %v calls=%v", account, err, upstream.calls)
	}
}

func walletCancellationFixture(t *testing.T) (*Service, *gorm.DB, *walletTestServer, *model.Task) {
	t.Helper()
	s, db, upstream, _ := newSharedWalletTest(t)
	if err := db.AutoMigrate(&model.TaskLog{}, &model.TaskTextDelta{}, &model.RouteAttempt{}, &model.Asset{}, &model.CanvasProject{}, &model.Result{}); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{ID: "cancel-reserve-task", UserID: "member", Type: "canvas_text", Status: model.TaskStatusRunning, Stage: "调用生成模型", InputJSON: `{}`, LeaseOwner: "worker", RouteID: "route", RouteRun: 1}
	until := time.Now().Add(time.Minute)
	task.LeaseExpiresAt = &until
	order := &model.BillingOrder{ID: "cancel-reserve-bill", UserID: task.UserID, TaskID: task.ID, IdempotencyKey: "cancel-reserve-bill", Model: "test-model", BillingMode: "fixed_request", AmountMicrocredits: 2000000, Status: model.BillingStatusReserved}
	if err := s.repo.ReserveBillingOrder(order); err != nil {
		t.Fatal(err)
	}
	task.BillingOrderID = order.ID
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.RouteAttempt{ID: "cancel-reserve-attempt", TaskID: task.ID, RouteRun: 1, RouteID: task.RouteID, DispatchState: "not_sent"}).Error; err != nil {
		t.Fatal(err)
	}
	s.activeCancels = map[string]context.CancelFunc{}
	return s, db, upstream, task
}

func assertCancelledWalletReview(t *testing.T, s *Service, id string) {
	t.Helper()
	order, err := s.repo.BillingOrder(id)
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != model.BillingStatusUncertain {
		t.Errorf("cancelled reservation lost review state: status=%s wallet=%s", order.Status, order.WalletState)
	}
	if order.WalletLockUntil != nil || order.WalletLockToken != "" {
		t.Error("wallet processing lock was not released")
	}
	if err := s.taskBilling().CheckRetryEligibility(id); !errors.Is(err, errTaskBillingReview) {
		t.Errorf("cancelled reservation no longer blocks duplicate charging: %v", err)
	}
}
