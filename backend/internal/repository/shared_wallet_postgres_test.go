package repository

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"yingce/backend/internal/model"
)

// Opt in with CANVAS_TEST_POSTGRES_DSN pointing at an isolated test database.
// Independent connections and pg_blocking_pids establish actual row contention;
// a passing SQLite interleaving alone cannot establish this locking contract.
func TestSharedWalletPostgresCancellationLocks(t *testing.T) {
	for _, action := range []string{"reserve", "running"} {
		t.Run("cancel_commits_before_"+action, func(t *testing.T) {
			a, b, observer := walletPostgresConnections(t)
			order, op := walletPostgresFixture(t, a)
			if action == "running" {
				walletPostgresOK(t, New(a).CompleteWalletOperation(op, "succeeded", `{}`, 98, 2, ""))
			}
			cancelTx := a.Begin()
			walletPostgresOK(t, cancelTx.Error)
			defer cancelTx.Rollback()
			walletPostgresCancel(t, cancelTx, order.TaskID)

			pid := walletPostgresPID(t, b)
			done := make(chan error, 1)
			go func() {
				if action == "reserve" {
					done <- New(b).CompleteWalletOperation(op, "succeeded", `{}`, 98, 2, "")
				} else {
					done <- New(b).MarkExternalWalletRunning(order.ID)
				}
			}()
			walletPostgresWaitBlocked(t, observer, pid, done)
			walletPostgresOK(t, cancelTx.Commit().Error)
			err := <-done
			if action == "running" {
				if !errors.Is(err, ErrBillingStateConflict) {
					t.Fatalf("cancelled task admitted: %v", err)
				}
			} else {
				walletPostgresOK(t, err)
			}
			walletPostgresAssert(t, b, order.ID, model.BillingStatusUncertain, 1)
		})
	}

	t.Run("rolled_back_cancellation_does_not_block_active_task", func(t *testing.T) {
		a, b, observer := walletPostgresConnections(t)
		order, op := walletPostgresFixture(t, a)
		cancelTx := a.Begin()
		walletPostgresOK(t, cancelTx.Error)
		defer cancelTx.Rollback()
		walletPostgresCancel(t, cancelTx, order.TaskID)
		pid := walletPostgresPID(t, b)
		done := make(chan error, 1)
		go func() { done <- New(b).CompleteWalletOperation(op, "succeeded", `{}`, 98, 2, "") }()
		walletPostgresWaitBlocked(t, observer, pid, done)
		walletPostgresOK(t, cancelTx.Rollback().Error)
		walletPostgresOK(t, <-done)
		walletPostgresOK(t, New(b).MarkExternalWalletRunning(order.ID))
		walletPostgresAssert(t, b, order.ID, model.BillingStatusRunning, 1)
	})

	t.Run("reserve_lock_blocks_cancel_until_receipt_commits", func(t *testing.T) {
		a, b, observer := walletPostgresConnections(t)
		order, op := walletPostgresFixture(t, a)
		receiptTx := a.Begin()
		walletPostgresOK(t, receiptTx.Error)
		defer receiptTx.Rollback()
		walletPostgresOK(t, New(receiptTx).CompleteWalletOperation(op, "succeeded", `{}`, 98, 2, ""))
		pid := walletPostgresPID(t, b)
		done := make(chan error, 1)
		go func() {
			cancelled, err := New(b).CancelTaskIfStatus("member", order.TaskID, model.TaskStatusRunning, time.Now())
			if err == nil && !cancelled {
				err = errors.New("cancellation did not update the running task")
			}
			if err == nil {
				err = New(b).MarkBillingUncertain(order.ID, "cancellation awaiting review")
			}
			done <- err
		}()
		walletPostgresWaitBlocked(t, observer, pid, done)
		walletPostgresOK(t, receiptTx.Commit().Error)
		walletPostgresOK(t, <-done)
		walletPostgresOK(t, New(b).CompleteWalletOperation(op, "succeeded", `{}`, 98, 2, ""))
		if !errors.Is(New(b).MarkExternalWalletRunning(order.ID), ErrBillingStateConflict) {
			t.Fatal("replayed reserve admitted a cancelled task")
		}
		walletPostgresAssert(t, b, order.ID, model.BillingStatusUncertain, 1)
	})

	t.Run("concurrent_receipts_write_one_ledger", func(t *testing.T) {
		a, b, observer := walletPostgresConnections(t)
		order, op := walletPostgresFixture(t, a)
		receiptTx := a.Begin()
		walletPostgresOK(t, receiptTx.Error)
		defer receiptTx.Rollback()
		walletPostgresOK(t, New(receiptTx).CompleteWalletOperation(op, "succeeded", `{}`, 98, 2, ""))
		pid := walletPostgresPID(t, b)
		done := make(chan error, 1)
		go func() { done <- New(b).CompleteWalletOperation(op, "succeeded", `{}`, 98, 2, "") }()
		walletPostgresWaitBlocked(t, observer, pid, done)
		walletPostgresOK(t, receiptTx.Commit().Error)
		walletPostgresOK(t, <-done)
		walletPostgresAssert(t, b, order.ID, model.BillingStatusReserved, 1)
	})

	t.Run("rebound_task_cannot_admit_old_bill", func(t *testing.T) {
		a, b, observer := walletPostgresConnections(t)
		order, op := walletPostgresFixture(t, a)
		walletPostgresOK(t, New(a).CompleteWalletOperation(op, "succeeded", `{}`, 98, 2, ""))
		retryTx := a.Begin()
		walletPostgresOK(t, retryTx.Error)
		defer retryTx.Rollback()
		walletPostgresOK(t, retryTx.Model(&model.Task{}).Where("id = ?", order.TaskID).Update("billing_order_id", "new-attempt").Error)
		pid := walletPostgresPID(t, b)
		done := make(chan error, 1)
		go func() { done <- New(b).MarkExternalWalletRunning(order.ID) }()
		walletPostgresWaitBlocked(t, observer, pid, done)
		walletPostgresOK(t, retryTx.Commit().Error)
		if err := <-done; !errors.Is(err, ErrBillingStateConflict) {
			t.Fatalf("old bill admitted new attempt: %v", err)
		}
		walletPostgresAssert(t, b, order.ID, model.BillingStatusUncertain, 1)
	})
}

func walletPostgresConnections(t *testing.T) (a, b, observer *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set CANVAS_TEST_POSTGRES_DSN to an isolated PostgreSQL database")
	}
	parsed, err := url.Parse(dsn)
	walletPostgresOK(t, err)
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		t.Fatal("test DSN must be a PostgreSQL URL")
	}
	open := func(raw string) *gorm.DB {
		db, err := gorm.Open(postgres.Open(raw), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		walletPostgresOK(t, err)
		pool, err := db.DB()
		walletPostgresOK(t, err)
		pool.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = pool.Close() })
		return db
	}
	observer = open(dsn)
	schema := fmt.Sprintf("wallet_lock_%d", time.Now().UnixNano())
	walletPostgresOK(t, observer.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		if err := observer.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Errorf("cleanup isolated schema: %v", err)
		}
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	query.Set("statement_timeout", "7000")
	parsed.RawQuery = query.Encode()
	a, b = open(parsed.String()), open(parsed.String())
	walletPostgresOK(t, a.AutoMigrate(&model.Task{}, &model.BillingOrder{}, &model.WalletOperation{}, &model.CreditLedgerEntry{}))
	return a, b, observer
}

func walletPostgresFixture(t *testing.T, db *gorm.DB) (model.BillingOrder, model.WalletOperation) {
	t.Helper()
	order := model.BillingOrder{ID: "bill", TaskID: "task", UserID: "member", IdempotencyKey: "bill",
		WalletMode: "external", WalletState: "pending", Status: model.BillingStatusUncertain,
		AmountMicrocredits: 2000000, ReservedAmountMicrocredits: 2000000}
	op := model.WalletOperation{ID: "reserve", BillingOrderID: order.ID, Action: "reserve", Point: 2, State: "pending", Attempts: 1}
	walletPostgresOK(t, db.Create(&model.Task{ID: order.TaskID, UserID: order.UserID, BillingOrderID: order.ID, Status: model.TaskStatusRunning}).Error)
	walletPostgresOK(t, db.Create(&order).Error)
	walletPostgresOK(t, db.Create(&op).Error)
	return order, op
}

func walletPostgresPID(t *testing.T, db *gorm.DB) int {
	t.Helper()
	var pid int
	walletPostgresOK(t, db.Raw("SELECT pg_backend_pid()").Scan(&pid).Error)
	return pid
}

func walletPostgresWaitBlocked(t *testing.T, observer *gorm.DB, pid int, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("operation completed before the competing task lock was released: %v", err)
		default:
		}
		var blocked bool
		walletPostgresOK(t, observer.Raw("SELECT cardinality(pg_blocking_pids(?)) > 0", pid).Scan(&blocked).Error)
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("PostgreSQL did not observe the expected row-lock contention")
}

func walletPostgresCancel(t *testing.T, db *gorm.DB, taskID string) {
	t.Helper()
	changed, err := New(db).CancelTaskIfStatus("member", taskID, model.TaskStatusRunning, time.Now())
	walletPostgresOK(t, err)
	if !changed {
		t.Fatal("fixture running task was not cancelled")
	}
}

func walletPostgresAssert(t *testing.T, db *gorm.DB, id string, status model.BillingStatus, ledgerCount int64) {
	t.Helper()
	order, err := New(db).BillingOrder(id)
	walletPostgresOK(t, err)
	if order.Status != status || order.WalletState != "reserved" {
		t.Fatalf("lost accounting state: status=%s wallet=%s", order.Status, order.WalletState)
	}
	var count int64
	walletPostgresOK(t, db.Model(&model.CreditLedgerEntry{}).Where("billing_order_id = ?", id).Count(&count).Error)
	if count != ledgerCount {
		t.Fatalf("ledger count=%d, want %d", count, ledgerCount)
	}
	var op model.WalletOperation
	walletPostgresOK(t, db.First(&op, "id = ?", "reserve").Error)
	if op.State != "succeeded" {
		t.Fatalf("confirmed receipt lost: %s", op.State)
	}
}

func walletPostgresOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
