package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

const walletScale int64 = 1000000

var ErrSharedWalletProbeMemberNotFound = errors.New("未找到匹配的共享钱包SSO会员")

func WalletPoints(micro int64) (int64, error) {
	if micro < 0 || micro > 99999999*walletScale {
		return 0, errors.New("积分金额超出共享钱包范围")
	}
	return (micro + walletScale - 1) / walletScale, nil
}

func (r *Repository) SharedWalletSettings() (model.SharedWalletSettings, error) {
	value := model.SharedWalletSettings{Mode: "internal"}
	// Isolated repositories (including legacy tools) may contain only finance tables.
	// The production server separately requires the complete versioned schema.
	if !r.db.Migrator().HasTable(&model.SystemSetting{}) {
		return value, nil
	}
	row, err := r.SystemSettingOptional(model.SharedWalletSettingKey)
	if err != nil || row == nil {
		return value, err
	}
	if err = json.Unmarshal([]byte(row.ValueJSON), &value); err != nil {
		return value, errors.New("共享钱包配置损坏")
	}
	if value.Mode != "internal" && value.Mode != "external" {
		return value, errors.New("共享钱包模式无效")
	}
	return value, nil
}

// A write lock on the singleton setting serializes settings changes with every
// admission, including repository transactions nested in Agent/Creation commits.
func lockSharedWallet(tx *gorm.DB) (model.SharedWalletSettings, error) {
	row := model.SystemSetting{Key: model.SharedWalletSettingKey, ValueJSON: `{"mode":"internal","version":0}`}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		// Do not probe the schema before the first write: SQLite WAL cannot
		// upgrade an already-read snapshot while another admission commits.
		if tx.Dialector.Name() == "sqlite" && strings.Contains(err.Error(), "no such table: system_settings") {
			return model.SharedWalletSettings{Mode: "internal"}, nil
		}
		return model.SharedWalletSettings{}, err
	}
	if err := tx.Model(&model.SystemSetting{}).Where("key = ?", model.SharedWalletSettingKey).UpdateColumn("updated_at", time.Now()).Error; err != nil {
		return model.SharedWalletSettings{}, err
	}
	return New(tx).SharedWalletSettings()
}

func (r *Repository) SaveSharedWalletSettings(next model.SharedWalletSettings, expected int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		current, err := lockSharedWallet(tx)
		if err != nil {
			return err
		}
		if current.Version != expected {
			return errors.New("钱包配置已变化，请刷新后重试")
		}
		var count int64
		if err = tx.Model(&model.BillingOrder{}).Where("status NOT IN ?", []model.BillingStatus{model.BillingStatusSettled, model.BillingStatusRefunded}).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return errors.New("存在未结清计费订单，不能切换钱包或凭证")
		}
		if tx.Migrator().HasTable(&model.Task{}) {
			if err = tx.Model(&model.Task{}).Where("status IN ?", []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning}).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return errors.New("存在排队或执行中的任务，不能切换钱包")
			}
		}
		if err = tx.Model(&model.WalletOperation{}).Where("state = ?", "pending").Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return errors.New("存在待确认钱包请求，请先对账")
		}
		if tx.Migrator().HasTable(&model.PaymentOrder{}) {
			if err = tx.Model(&model.PaymentOrder{}).Where("status NOT IN ?", []string{"credited", "closed", "create_failed"}).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return errors.New("存在未结束充值订单，请先处理")
			}
		}
		next.Version = current.Version + 1
		raw, err := json.Marshal(next)
		if err != nil {
			return err
		}
		return tx.Model(&model.SystemSetting{}).Where("key = ?", model.SharedWalletSettingKey).Update("value_json", string(raw)).Error
	})
}

func (r *Repository) SharedWalletMember(userID string, cfg model.SharedWalletSettings) (int64, error) {
	sum := sha256.Sum256([]byte(cfg.Issuer))
	prefix := hex.EncodeToString(sum[:]) + ":" + strconv.FormatInt(cfg.SiteID, 10) + ":"
	var identities []model.UserIdentity
	if err := r.db.Where("user_id = ? AND provider = ?", userID, "cloooud").Find(&identities).Error; err != nil {
		return 0, err
	}
	var memberID int64
	for _, identity := range identities {
		if !strings.HasPrefix(identity.Subject, prefix) {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(identity.Subject, prefix), 10, 64)
		if err != nil || id <= 0 {
			return 0, errors.New("小洞会员绑定无效")
		}
		if memberID != 0 && memberID != id {
			return 0, errors.New("小洞会员绑定不唯一")
		}
		memberID = id
	}
	if memberID == 0 {
		return 0, errors.New("请从小洞AI免登后使用共享积分")
	}
	return memberID, nil
}

func (r *Repository) SharedWalletProbeMember(cfg model.SharedWalletSettings) (int64, error) {
	var identities []model.UserIdentity
	if err := r.db.Where("provider = ?", "cloooud").Find(&identities).Error; err != nil {
		return 0, err
	}
	for _, identity := range identities {
		if id, err := r.SharedWalletMember(identity.UserID, cfg); err == nil {
			return id, nil
		}
	}
	return 0, ErrSharedWalletProbeMemberNotFound
}

func prepareSharedWalletAdmission(tx *gorm.DB, order *model.BillingOrder) (bool, error) {
	cfg, err := lockSharedWallet(tx)
	if err != nil {
		return false, err
	}
	if order.WalletMode != "" && (order.WalletMode != cfg.Mode || order.WalletVersion != cfg.Version) {
		return false, errors.New("钱包配置已变化，请重新报价")
	}
	order.WalletMode, order.WalletVersion = cfg.Mode, cfg.Version
	if cfg.Mode != "external" {
		return false, nil
	}
	if cfg.BaseURL == "" || cfg.SecretCipher == "" || cfg.ClientID == "" || cfg.SiteID <= 0 || cfg.Issuer == "" {
		return false, errors.New("外部钱包配置不完整")
	}
	memberID, err := New(tx).SharedWalletMember(order.UserID, cfg)
	if err != nil {
		return false, err
	}
	// External token orders require an explicit cap. A provider must never be
	// allowed to charge an unbounded amount against a finite shared reservation.
	if order.BillingMode == "per_second" && order.Capability == "audio" {
		return false, errors.New("外部钱包不支持无输出时长上限的按秒音频")
	}
	if order.BillingMode == "token" && (!order.ChargeLimitSet || (order.Capability == "text" && order.WalletOutputTokenLimit <= 0)) {
		return false, errors.New("外部钱包Token调用缺少可执行上限或授权金额")
	}
	amount := order.AmountMicrocredits
	if order.ChargeLimitSet && order.ChargeLimitMicrocredits > amount {
		amount = order.ChargeLimitMicrocredits
	}
	point, err := WalletPoints(amount)
	if err != nil {
		return false, err
	}
	estimatedPoint, err := WalletPoints(order.AmountMicrocredits)
	if err != nil {
		return false, err
	}
	order.AmountMicrocredits = estimatedPoint * walletScale
	order.ReservedAmountMicrocredits = point * walletScale
	order.WalletMemberID = memberID
	order.WalletState = "pending"
	raw, err := json.Marshal(cfg)
	if err != nil {
		return false, err
	}
	order.WalletConfigJSON = string(raw)
	if err = tx.Create(order).Error; err != nil {
		return false, err
	}
	op := model.WalletOperation{ID: newRepositoryID(), BillingOrderID: order.ID, Action: "reserve", Point: point, State: "pending"}
	return true, tx.Create(&op).Error
}

func (r *Repository) RequireInternalWallet() error {
	cfg, err := r.SharedWalletSettings()
	if err != nil {
		return err
	}
	if cfg.Mode == "external" {
		return errors.New("外部钱包模式请在小洞AI管理积分")
	}
	return nil
}

// Reward and local mutations share the admission lock, so no concurrent mode
// switch can slip between checking the mode and committing local funds.
func requireInternalWalletTransaction(tx *gorm.DB) error {
	cfg, err := lockSharedWallet(tx)
	if err != nil {
		return err
	}
	if cfg.Mode == "external" {
		return errors.New("外部钱包模式不支持本地积分操作")
	}
	return nil
}

func (r *Repository) ClaimWalletOrder(id, token string) (bool, error) {
	now := time.Now()
	until := now.Add(time.Minute)
	out := r.db.Model(&model.BillingOrder{}).Where("id = ? AND wallet_mode = ? AND (wallet_lock_until IS NULL OR wallet_lock_until < ?)", id, "external", now).Updates(map[string]any{"wallet_lock_until": until, "wallet_lock_token": token})
	return out.RowsAffected == 1, out.Error
}
func (r *Repository) ReleaseWalletOrder(id, token string) error {
	return r.db.Model(&model.BillingOrder{}).Where("id = ? AND wallet_lock_token = ?", id, token).Updates(map[string]any{"wallet_lock_until": nil, "wallet_lock_token": ""}).Error
}
func (r *Repository) WalletOperations(id string) ([]model.WalletOperation, error) {
	var ops []model.WalletOperation
	err := r.db.Where("billing_order_id = ?", id).Order("created_at asc, id asc").Find(&ops).Error
	return ops, err
}
func (r *Repository) PendingWalletOrders() ([]string, error) {
	var ids []string
	// Never precharge an untouched queued task in the reconciler. Only requests
	// attempted by a worker or a terminal operation need network reconciliation.
	pending := r.db.Model(&model.WalletOperation{}).Select("billing_order_id").
		Where("state = ? AND (attempts > 0 OR action <> ?)", "pending", "reserve")
	// Claim/release updates the order even when its snapshot fails before an
	// operation is attempted. Oldest-first selection lets later orders progress.
	err := r.db.Model(&model.BillingOrder{}).Where("id IN (?)", pending).
		Where("wallet_mode = ? AND (wallet_lock_until IS NULL OR wallet_lock_until < ?)", "external", time.Now()).
		Order("updated_at asc, id asc").Limit(50).Pluck("id", &ids).Error
	return ids, err
}

func (r *Repository) RejectedWalletRestores() ([]string, error) {
	var ids []string
	err := r.db.Model(&model.WalletOperation{}).Where("action = ? AND state = ? AND error = ?", "restore", "rejected", "insufficient").
		Order("updated_at asc, id asc").Limit(50).Pluck("billing_order_id", &ids).Error
	return ids, err
}
func (r *Repository) AttemptWalletOperation(id string) error {
	return r.db.Model(&model.WalletOperation{}).Where("id = ? AND state = ?", id, "pending").Updates(map[string]any{"attempts": gorm.Expr("attempts + 1"), "updated_at": time.Now()}).Error
}
func (r *Repository) FailWalletOperation(id, reason string) error {
	return r.db.Model(&model.WalletOperation{}).Where("id = ? AND state = ?", id, "pending").Updates(map[string]any{"error": reason, "updated_at": time.Now()}).Error
}

func (r *Repository) QueueWalletOperation(id, action string, duration *int64) (*model.WalletOperation, error) {
	var result model.WalletOperation
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// This no-op write serializes terminal operation intentions for one order.
		if err := tx.Model(&model.BillingOrder{}).Where("id = ?", id).UpdateColumn("updated_at", time.Now()).Error; err != nil {
			return err
		}
		var order model.BillingOrder
		if err := tx.First(&order, "id = ?", id).Error; err != nil {
			return err
		}
		if order.WalletMode != "external" {
			return errors.New("not an external wallet order")
		}
		if action == "refund" && order.Status == model.BillingStatusSettled {
			return errors.New("已结算账单不能按失败退款")
		}
		if err := tx.Where("billing_order_id = ? AND action = ?", id, action).First(&result).Error; err == nil {
			if action == "restore" && result.State == "rejected" && result.Error == "insufficient" && order.Status == model.BillingStatusRefunded {
				if err := tx.Model(&result).Updates(map[string]any{"state": "pending", "error": ""}).Error; err != nil {
					return err
				}
				result.State = "pending"
				result.Error = ""
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if action == "reserve" {
			return errors.New("预扣意图不存在")
		}
		if action == "restore" && order.Status != model.BillingStatusRefunded {
			return ErrBillingStateConflict
		}
		if action == "refund" && order.Status == model.BillingStatusSettled {
			return errors.New("已结算账单不能按失败退款")
		}
		if action == "settle" && order.Status == model.BillingStatusRefunded {
			return ErrBillingStateConflict
		}
		var pending int64
		if err := tx.Model(&model.WalletOperation{}).Where("billing_order_id = ? AND action <> ? AND state = ?", id, "reserve", "pending").Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return errors.New("已有钱包请求待确认，请先对账")
		}
		point := int64(0)
		if action == "settle" || action == "restore" {
			actual := order.AmountMicrocredits
			if order.BillingMode == "token" && !zeroPricedTokenOrder(order) {
				usage, source, err := tokenSettlementUsage(tx, order)
				if err != nil {
					return err
				}
				if err = tx.Model(&order).Updates(map[string]any{"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "cached_tokens": usage.CachedTokens, "usage_available": source == billingUsageSourceProvider, "usage_source": source}).Error; err != nil {
					return err
				}
				actual, err = tokenUsageAmount(order, usage)
				if err != nil {
					return err
				}
			} else if order.BillingMode == "per_second" && order.Capability == "audio" {
				if duration == nil || *duration <= 0 {
					return errors.New("音频实际时长不可用")
				}
				var err error
				actual, err = audioBillingAmount(order.UnitPriceMicrocredits, (*duration+999)/1000, order.MultiplierBasisPoints)
				if err != nil {
					return err
				}
			}
			if actual > order.ReservedAmountMicrocredits || (order.ChargeLimitSet && actual > order.ChargeLimitMicrocredits) {
				return ErrBillingChargeLimit
			}
			var err error
			point, err = WalletPoints(actual)
			if err != nil {
				return err
			}
		}
		result = model.WalletOperation{ID: newRepositoryID(), BillingOrderID: id, Action: action, Point: point, State: "pending"}
		return tx.Create(&result).Error
	})
	return &result, err
}

func requireNonbillableTaskAdmission(tx *gorm.DB, task *model.Task) error {
	cfg, err := lockSharedWallet(tx)
	if err != nil {
		return err
	}
	if cfg.Mode != "external" {
		return nil
	}
	if task.Operation == "cloud_agent" && task.Status == model.TaskStatusTextReplay {
		return nil
	}
	var input struct {
		Config struct {
			ChannelID string `json:"channelId"`
			BaseURL   string `json:"baseUrl"`
		} `json:"config"`
	}
	if err = json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return errors.New("无法确认免费任务的渠道来源")
	}
	if task.LogicalModelID != "" || task.ChannelModelID != "" || input.Config.ChannelID != "" || strings.Contains(input.Config.BaseURL, "/api/ai/system/") {
		return errors.New("外部钱包模式要求计费订单，请重新提交")
	}
	return nil
}

func (r *Repository) CompleteWalletOperation(op model.WalletOperation, state string, response string, balance, charged int64, reason string) error {
	snapshot, err := r.BillingOrder(op.BillingOrderID)
	if err != nil {
		return err
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var task *model.Task
		if op.Action == "reserve" {
			var err error
			task, err = lockWalletBillingTask(tx, snapshot)
			if err != nil {
				return err
			}
		}
		updated := tx.Model(&model.WalletOperation{}).Where("id = ? AND state = ?", op.ID, "pending").Updates(map[string]any{"state": state, "response_json": response, "error": reason, "updated_at": time.Now()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return nil
		}
		var order model.BillingOrder
		if err := tx.First(&order, "id = ?", op.BillingOrderID).Error; err != nil {
			return err
		}
		updates := map[string]any{"updated_at": time.Now()}
		if state == "rejected" {
			updates["wallet_state"] = "rejected"
			updates["error"] = "共享钱包拒绝操作：" + reason
			if op.Action == "reserve" || (op.Action == "restore" && reason == "insufficient") {
				updates["status"] = model.BillingStatusRefunded
			} else {
				updates["status"] = model.BillingStatusUncertain
			}
			return tx.Model(&order).Updates(updates).Error
		}
		now := time.Now()
		amount := int64(0)
		entryType := model.CreditLedgerReserve
		switch op.Action {
		case "reserve":
			updates["wallet_state"] = "reserved"
			if !walletBillingTaskCanRun(task, order) {
				updates["status"] = model.BillingStatusUncertain
				updates["error"] = "任务已停止，已确认的共享钱包预扣需核对"
			} else if order.Status == model.BillingStatusUncertain {
				updates["status"] = model.BillingStatusReserved
			}
		case "settle", "restore":
			updates["wallet_state"] = "settled"
			updates["status"] = model.BillingStatusSettled
			updates["actual_amount_microcredits"] = charged * walletScale
			updates["settled_at"] = &now
			updates["refunded_amount_microcredits"] = max(order.ReservedAmountMicrocredits-charged*walletScale, 0)
			entryType = model.CreditLedgerConsume
			amount = -charged * walletScale
		case "refund":
			updates["wallet_state"] = "refunded"
			updates["status"] = model.BillingStatusRefunded
			updates["refunded_at"] = &now
			updates["refunded_amount_microcredits"] = order.ReservedAmountMicrocredits
			entryType = model.CreditLedgerRefund
			amount = order.ReservedAmountMicrocredits
			var reserved int64
			if err := tx.Model(&model.WalletOperation{}).Where("billing_order_id = ? AND action = ? AND state = ?", order.ID, "reserve", "succeeded").Count(&reserved).Error; err != nil {
				return err
			}
			if reserved == 0 {
				amount = 0
				updates["refunded_amount_microcredits"] = int64(0)
			}
		default:
			return fmt.Errorf("unknown wallet action %q", op.Action)
		}
		if err := tx.Model(&order).Updates(updates).Error; err != nil {
			return err
		}
		if op.Action == "refund" && amount == 0 {
			return nil
		}
		reference := "wallet:" + op.ID
		return tx.Create(&model.CreditLedgerEntry{ID: newRepositoryID(), UserID: order.UserID, Type: entryType, AmountMicrocredits: amount, AvailableAfterMicrocredits: balance * walletScale, BillingOrderID: order.ID, Model: order.Model, Scene: order.Scene, ReferenceKey: &reference, Note: "共享积分"}).Error
	})
}

func (r *Repository) MarkExternalWalletRunning(id string) error {
	snapshot, err := r.BillingOrder(id)
	if err != nil {
		return err
	}
	stopped := false
	err = r.db.Transaction(func(tx *gorm.DB) error {
		task, err := lockWalletBillingTask(tx, snapshot)
		if err != nil {
			return err
		}
		if err := tx.Model(&model.BillingOrder{}).Where("id = ?", id).UpdateColumn("updated_at", time.Now()).Error; err != nil {
			return err
		}
		var order model.BillingOrder
		if err := tx.First(&order, "id = ?", id).Error; err != nil {
			return err
		}
		if !walletBillingTaskCanRun(task, order) {
			stopped = true
			return New(tx).MarkBillingUncertain(id, "任务已停止，已确认的共享钱包预扣需核对")
		}
		if order.WalletState != "reserved" {
			return ErrBillingStateConflict
		}
		var pending int64
		if err := tx.Model(&model.WalletOperation{}).Where("billing_order_id = ? AND action <> ?", id, "reserve").Count(&pending).Error; err != nil {
			return err
		}
		if pending != 0 {
			return ErrBillingStateConflict
		}
		return New(tx).MarkBillingRunning(id)
	})
	if err == nil && stopped {
		return ErrBillingStateConflict
	}
	return err
}

// Task identity is fixed at billing admission. Lock the task before the order,
// matching task cancellation/route updates and avoiding a read/check/write gap.
// The first statement is a write so SQLite can acquire its writer lock before
// reading a snapshot; PostgreSQL holds the same row lock until the commit.
func lockWalletBillingTask(tx *gorm.DB, order *model.BillingOrder) (*model.Task, error) {
	if order.TaskID == "" {
		return nil, nil
	}
	if err := tx.Model(&model.Task{}).Where("id = ?", order.TaskID).
		UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
		return nil, err
	}
	var task model.Task
	if err := tx.First(&task, "id = ?", order.TaskID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &task, nil
		}
		return nil, err
	}
	return &task, nil
}

func walletBillingTaskCanRun(task *model.Task, order model.BillingOrder) bool {
	return task == nil || (task.UserID == order.UserID && task.BillingOrderID == order.ID && task.Status == model.TaskStatusRunning)
}

func (r *Repository) ExternalWalletLedger(userID, entryType string, limit, offset int) ([]model.CreditLedgerEntry, int64, error) {
	var entries []model.CreditLedgerEntry
	var total int64
	q := r.db.Model(&model.CreditLedgerEntry{}).Where("user_id = ? AND reference_key LIKE ? AND type <> ?", userID, "wallet:%", model.CreditLedgerReserve)
	if entryType == "consume" || entryType == "refund" {
		q = q.Where("type = ?", entryType)
	} else if entryType == "income" {
		q = q.Where("1 = 0")
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := q.Order("created_at desc").Limit(limit).Offset(offset).Find(&entries).Error
	return entries, total, err
}
