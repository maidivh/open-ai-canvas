package app

import (
	"context"

	"yingce/backend/internal/model"
)

// Completion and its wallet intention are committed together. Network failures
// leave that immutable intention for the shared wallet reconciler to replay.
func (s *Service) finishEcommerceAgentRun(userID, runID, status, errorCode string) (*model.EcommerceAgentRun, error) {
	run, err := s.repo.FinishEcommerceAgentRun(userID, runID, status, errorCode)
	if err != nil || run.BillingOrderID == "" {
		return run, err
	}
	order, err := s.repo.BillingOrder(run.BillingOrderID)
	if err != nil {
		return run, err
	}
	if order.WalletMode == "external" {
		err = s.processWalletOrder(context.Background(), order.ID)
	}
	return run, err
}
