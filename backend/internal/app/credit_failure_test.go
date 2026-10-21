package app

import (
	"fmt"
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestCreditFailureMessagesIdentifyOriginalWallet(t *testing.T) {
	for _, raw := range []string{"积分不足，请先使用兑换码充值", "积分不足，请充值后重试", "积分不足，请先充值", "模型服务价格发生变化，当前积分余额不足"} {
		message := userFacingTaskError(raw)
		if !strings.Contains(message, "该次请求") || !strings.Contains(message, "内部积分不足") || !strings.Contains(message, "当前扣费模式") || strings.Contains(message, "兑换码") {
			t.Errorf("historical internal-wallet failure remains misleading: %q", message)
		}
	}
	for _, err := range []error{creationError(repository.ErrInsufficientCredits), fmt.Errorf("wrapped: %w", repository.ErrInsufficientCredits)} {
		message := cloudAgentUserFailureMessage("credit-failure", err)
		if !strings.Contains(message, "内部积分不足") || strings.Contains(message, "兑换码") {
			t.Errorf("creation/Agent lost the wallet source: %q", message)
		}
	}
	message := userFacingTaskError("共享钱包拒绝操作：insufficient")
	if !strings.Contains(message, "外部钱包") || !strings.Contains(message, "积分不足") || !strings.Contains(message, "订单状态") || strings.Contains(message, "insufficient") || strings.Contains(message, "已退") {
		t.Errorf("external-wallet failure remains misleading: %q", message)
	}
}

func TestCreditFailureAgentSnapshotKeepsHistoryUnmodified(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	const original = "积分不足，请先使用兑换码充值"
	task := &model.Task{ID: "historical-credit-failure", UserID: "user"}
	stored := model.CloudAgentExecution{ID: task.ID, UserID: task.UserID, Status: "failed", FailureMessage: original, StateJSON: "{}"}
	if err := db.Create(&stored).Error; err != nil {
		t.Fatal(err)
	}
	out, err := s.cloudAgentExecutionOutput(task, cloudAgentState{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.FailureMessage, "该次请求") || !strings.Contains(out.FailureMessage, "内部积分不足") {
		t.Fatalf("historical snapshot exposes the old recharge hint: %q", out.FailureMessage)
	}
	if err := db.First(&stored, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.FailureMessage != original {
		t.Fatal("reading a snapshot rewrote the stored historical failure")
	}
}
