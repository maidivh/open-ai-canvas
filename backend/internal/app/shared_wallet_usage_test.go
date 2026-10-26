package app

import (
	"fmt"
	"testing"

	"yingce/backend/internal/model"
)

func TestSharedWalletVideoProcessingSettlesMeasuredDuration(t *testing.T) {
	for _, test := range []struct {
		name        string
		processing  bool
		seconds     float64
		wantPoints  int64
		wantSeconds int64
		wantError   bool
	}{
		{name: "shorter output refunds difference", processing: true, seconds: 2.5, wantPoints: 5, wantSeconds: 3},
		{name: "full output retains reservation", processing: true, seconds: 10, wantPoints: 15, wantSeconds: 10},
		{name: "overrun requires review", processing: true, seconds: 11, wantError: true},
		{name: "missing duration requires review", processing: true, wantError: true},
		{name: "ordinary generation retains requested duration", seconds: 2.5, wantPoints: 15, wantSeconds: 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, db, upstream, user := newSharedWalletTest(t)
			order := &model.BillingOrder{
				ID: "video-duration", UserID: user.ID, IdempotencyKey: "video-duration", Capability: "video", BillingMode: "per_second",
				Quantity: 10, UnitPriceMicrocredits: 1250000, MultiplierBasisPoints: 12000,
				BillingCostSnapshot: model.BillingCostSnapshot{CostBillingMode: "per_second", CostQuantity: 10},
				AmountMicrocredits:  15000000, Status: model.BillingStatusReserved,
			}
			if err := s.repo.ReserveBillingOrder(order); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkBillingRunning(order.ID); err != nil {
				t.Fatal(err)
			}
			task := model.Task{
				UserID: user.ID, Type: "video", BillingOrderID: order.ID,
				InputJSON:  fmt.Sprintf(`{"metadata":{"videoProcessing":%t}}`, test.processing),
				ResultJSON: fmt.Sprintf(`{"usage":{"outputDurationSeconds":%g}}`, test.seconds),
			}
			for range 2 {
				handled, err := s.settleVideoProcessingBilling(task, "provider-video")
				if handled != test.processing {
					t.Fatalf("handled=%v, want %v", handled, test.processing)
				}
				if !handled && err == nil {
					err = s.SettleBilling(order.ID, "provider-video")
				}
				if (err != nil) != test.wantError {
					t.Fatalf("settlement error=%v, wantError=%v", err, test.wantError)
				}
			}
			saved, err := s.repo.BillingOrder(order.ID)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantError {
				if saved.Status == model.BillingStatusSettled || upstream.calls["settle"] != 0 || upstream.balance != 85 {
					t.Fatalf("invalid output settled: status=%s calls=%d balance=%d", saved.Status, upstream.calls["settle"], upstream.balance)
				}
				if saved.Quantity != 10 || saved.CostQuantity != 10 {
					t.Fatalf("rejected settlement changed quantities: quantity=%d cost=%d", saved.Quantity, saved.CostQuantity)
				}
				var operations int64
				if err := db.Model(&model.WalletOperation{}).Where("billing_order_id = ? AND action = ?", order.ID, "settle").Count(&operations).Error; err != nil || operations != 0 {
					t.Fatalf("rejected settlement created an intent: count=%d err=%v", operations, err)
				}
				return
			}
			if saved.Status != model.BillingStatusSettled || saved.ActualAmountMicrocredits != test.wantPoints*CreditScale || saved.RefundedAmountMicrocredits != (15-test.wantPoints)*CreditScale {
				t.Fatalf("unexpected settlement: status=%s actual=%d refund=%d", saved.Status, saved.ActualAmountMicrocredits, saved.RefundedAmountMicrocredits)
			}
			if upstream.balance != 100-test.wantPoints || upstream.calls["settle"] != 1 {
				t.Fatalf("settlement not idempotent: balance=%d calls=%d", upstream.balance, upstream.calls["settle"])
			}
			if saved.Quantity != test.wantSeconds || saved.CostQuantity != test.wantSeconds {
				t.Fatalf("billing quantities do not match output: quantity=%d cost=%d want=%d", saved.Quantity, saved.CostQuantity, test.wantSeconds)
			}
			account, err := s.repo.CreditAccount(user.ID)
			if err != nil || account.AvailableMicrocredits != 50000000 {
				t.Fatalf("external settlement changed internal balance: account=%+v err=%v", account, err)
			}
		})
	}
}

func TestSharedWalletVideoTokenSettlementRecordsUsageSource(t *testing.T) {
	for _, action := range []string{"settle", "restore"} {
		for _, test := range []struct {
			name          string
			log           *model.ApiCallLog
			wantAvailable bool
			wantSource    string
			wantTokens    int64
			wantPoints    int64
		}{
			{name: "missing provider usage", wantSource: "video_formula", wantTokens: 108000, wantPoints: 2},
			{name: "zero provider usage", log: &model.ApiCallLog{UsageAvailable: true}, wantSource: "video_formula", wantTokens: 108000, wantPoints: 2},
			{name: "unavailable provider usage", log: &model.ApiCallLog{OutputTokens: 187500}, wantSource: "video_formula", wantTokens: 108000, wantPoints: 2},
			{name: "valid provider usage", log: &model.ApiCallLog{UsageAvailable: true, OutputTokens: 187500, InputTokens: 20, CachedTokens: 10}, wantAvailable: true, wantSource: "provider", wantTokens: 187500, wantPoints: 3},
		} {
			t.Run(action+"/"+test.name, func(t *testing.T) {
				s, db, upstream, user := newSharedWalletTest(t)
				order := &model.BillingOrder{
					ID: "video-usage", UserID: user.ID, IdempotencyKey: "video-usage", Capability: "video", BillingMode: "token",
					Quantity: 118800, VideoFormulaTokens: 108000, AmountMicrocredits: 3000000,
					OutputTokenPriceMicrocredits: 16000000, MultiplierBasisPoints: 10000,
					ChargeLimitSet: true, ChargeLimitMicrocredits: 3000000, Status: model.BillingStatusReserved,
				}
				if err := s.repo.ReserveBillingOrder(order); err != nil {
					t.Fatal(err)
				}
				if err := s.MarkBillingRunning(order.ID); err != nil {
					t.Fatal(err)
				}
				if test.log != nil {
					log := *test.log
					log.ID, log.BillingOrderID, log.Status = "video-usage-log", order.ID, model.ApiCallStatusSucceeded
					if err := db.Create(&log).Error; err != nil {
						t.Fatal(err)
					}
				}
				settle := s.SettleBilling
				if action == "restore" {
					if err := s.RefundBilling(order.ID, "provider result arrived after refund"); err != nil {
						t.Fatal(err)
					}
					settle = s.taskBilling().RestoreRefundedBilling
				}
				for i := 0; i < 2; i++ {
					if err := settle(order.ID, "provider-video"); err != nil {
						t.Fatalf("%s attempt %d: %v", action, i+1, err)
					}
				}
				saved, err := s.repo.BillingOrder(order.ID)
				if err != nil {
					t.Fatal(err)
				}
				if saved.UsageAvailable != test.wantAvailable || saved.UsageSource != test.wantSource {
					t.Errorf("usage available=%v source=%q, want available=%v source=%q", saved.UsageAvailable, saved.UsageSource, test.wantAvailable, test.wantSource)
				}
				if saved.InputTokens != 0 || saved.CachedTokens != 0 || saved.OutputTokens != test.wantTokens {
					t.Errorf("usage input=%d cached=%d output=%d, want input=0 cached=0 output=%d", saved.InputTokens, saved.CachedTokens, saved.OutputTokens, test.wantTokens)
				}
				if saved.Status != model.BillingStatusSettled || saved.ActualAmountMicrocredits != test.wantPoints*CreditScale || saved.RefundedAmountMicrocredits != 3000000-test.wantPoints*CreditScale {
					t.Errorf("settlement status=%s amount=%d refund=%d", saved.Status, saved.ActualAmountMicrocredits, saved.RefundedAmountMicrocredits)
				}
				if saved.VideoFormulaTokens != 108000 || saved.Quantity != 118800 || saved.ReservedAmountMicrocredits != 3000000 {
					t.Errorf("reservation snapshot changed: formula=%d quantity=%d reserved=%d", saved.VideoFormulaTokens, saved.Quantity, saved.ReservedAmountMicrocredits)
				}
				if upstream.balance != 100-test.wantPoints || upstream.calls[action] != 1 {
					t.Errorf("external settlement balance=%d calls=%d, want balance=%d calls=1", upstream.balance, upstream.calls[action], 100-test.wantPoints)
				}
				var operations []model.WalletOperation
				if err := db.Where("billing_order_id = ? AND action = ?", order.ID, action).Find(&operations).Error; err != nil {
					t.Fatal(err)
				}
				if len(operations) != 1 || operations[0].State != "succeeded" || operations[0].Point != test.wantPoints {
					t.Errorf("unexpected wallet operations: %+v", operations)
				}
				var entries []model.CreditLedgerEntry
				if err := db.Where("billing_order_id = ? AND type = ?", order.ID, model.CreditLedgerConsume).Find(&entries).Error; err != nil {
					t.Fatal(err)
				}
				if len(entries) != 1 || entries[0].AmountMicrocredits != -test.wantPoints*CreditScale {
					t.Errorf("unexpected consumption entries: %+v", entries)
				}
			})
		}
	}
}
