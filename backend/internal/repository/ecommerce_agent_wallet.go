package repository

import (
	"errors"

	"gorm.io/gorm"
	"yingce/backend/internal/ecommerceagent"
	"yingce/backend/internal/model"
)

// Ecommerce runs own their identity; a same-ID canvas task cannot authorize them.
func lockEcommerceWalletRun(tx *gorm.DB, order *model.BillingOrder) (*walletBillingSubject, error) {
	if order.TaskID == "" {
		return &walletBillingSubject{}, nil
	}
	if err := tx.Model(&model.EcommerceAgentRun{}).Where("id = ?", order.TaskID).UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
		return nil, err
	}
	var run model.EcommerceAgentRun
	if err := tx.First(&run, "id = ?", order.TaskID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &walletBillingSubject{}, nil
		}
		return nil, err
	}
	return &walletBillingSubject{UserID: run.UserID, BillingOrderID: run.BillingOrderID, Running: run.Status == "running" && !run.CancelRequested}, nil
}

func ecommerceWalletSettlementAmount(tx *gorm.DB, order *model.BillingOrder) (int64, error) {
	var run model.EcommerceAgentRun
	if err := tx.First(&run, "id = ? AND user_id = ? AND billing_order_id = ?", order.TaskID, order.UserID, order.ID).Error; err != nil {
		return 0, err
	}
	if run.Status != "completed" && run.Status != "cancelled" && run.Status != "failed" {
		return 0, ErrBillingStateConflict
	}
	reserved, actual, _, err := ecommerceagent.Charge(run.UnitMicrocredits, run.PageCount, run.SuccessPages)
	if err != nil {
		return 0, err
	}
	points, err := WalletPoints(reserved)
	if err != nil {
		return 0, err
	}
	if order.BillingMode != "fixed_request" || order.ReservedAmountMicrocredits != points*walletScale || order.AmountMicrocredits != points*walletScale {
		return 0, errors.New("ecommerce billing snapshot mismatch")
	}
	if err = tx.Model(order).Updates(map[string]any{"quantity": run.SuccessPages, "cost_quantity": run.SuccessPages}).Error; err != nil {
		return 0, err
	}
	return actual, nil
}
