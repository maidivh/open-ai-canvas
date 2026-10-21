package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/outbound"
	"yingce/backend/internal/repository"
)

type WalletSettingsView struct {
	Mode             string `json:"mode"`
	BaseURL          string `json:"baseUrl"`
	ClientID         string `json:"clientId"`
	SiteID           int64  `json:"siteId"`
	Issuer           string `json:"issuer"`
	RechargeURL      string `json:"rechargeUrl"`
	SecretConfigured bool   `json:"secretConfigured"`
	Version          int64  `json:"version"`
}
type WalletSettingsRequest struct {
	WalletSettingsView
	ClientSecret string `json:"clientSecret"`
}

func walletSettingsView(c model.SharedWalletSettings) WalletSettingsView {
	return WalletSettingsView{Mode: c.Mode, BaseURL: c.BaseURL, ClientID: c.ClientID, SiteID: c.SiteID, Issuer: c.Issuer, RechargeURL: c.RechargeURL, SecretConfigured: c.SecretCipher != "", Version: c.Version}
}

func walletQuoteAmount(amount int64, cfg model.SharedWalletSettings) (int64, error) {
	if cfg.Mode == "external" {
		point, e := repository.WalletPoints(amount)
		return point * CreditScale, e
	}
	return amount, nil
}

// Estimation, rounding and admission must all refer to the original snapshot.
// Admission rejects its version if the wallet configuration changes meanwhile.
func prepareWalletQuote(order *model.BillingOrder, cfg model.SharedWalletSettings) (*model.BillingOrder, error) {
	amount, err := walletQuoteAmount(order.AmountMicrocredits, cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Mode == "external" {
		if order.BillingMode == "per_second" && order.Capability == "audio" {
			return nil, BadAuthRequest("外部钱包暂不支持无输出时长上限的按秒音频，请选择固定价格模型")
		}
		if order.BillingMode == "token" && order.Capability == "text" && order.WalletOutputTokenLimit <= 0 {
			return nil, BadAuthRequest("外部钱包要求明确的文本输出Token上限")
		}
		if order.BillingMode == "token" {
			order.ChargeLimitSet = true
			order.ChargeLimitMicrocredits = amount
		}
	}
	order.AmountMicrocredits = amount
	order.ReservedAmountMicrocredits = amount
	order.WalletMode = cfg.Mode
	order.WalletVersion = cfg.Version
	return order, nil
}

// Freeze the same executable output bound used by the provider into the bill.
// A guessed default in an estimate is never a permission for an unbounded call.
func walletTokenEstimate(estimate tokenBillingEstimate, item *model.ChannelModel, mode string, cfg model.SharedWalletSettings) (tokenBillingEstimate, error) {
	if cfg.Mode != "external" {
		return estimate, nil
	}
	if mode == "per_second" && item.Capability == "audio" {
		return estimate, BadAuthRequest("外部钱包暂不支持无输出时长上限的按秒音频，请选择固定价格模型")
	}
	if mode != "token" {
		return estimate, nil
	}
	if estimate.InvalidMultiplicity {
		return estimate, BadAuthRequest("外部钱包Token请求仅支持单个输出，n/best_of必须为1")
	}
	if item.Capability == "video" {
		if estimate.Video == nil || estimate.Video.DimensionsEstimated || estimate.Video.ReferenceDurationEstimated || estimate.Video.ReferenceSeconds > 0 {
			return estimate, BadAuthRequest("外部钱包视频Token计费目前仅支持确定尺寸且不含参考视频的请求")
		}
		return estimate, nil
	}
	if item.Capability != "text" {
		return estimate, BadAuthRequest("外部钱包不支持当前Token计费能力")
	}
	switch string(item.Protocol) {
	case "chat-completion", "openai-response", "claude-api":
	default:
		return estimate, BadAuthRequest("外部钱包Token计费仅支持可执行输出上限的标准文本协议")
	}
	if estimate.Proxy {
		key := "max_tokens"
		if string(item.Protocol) == "openai-response" {
			key = "max_output_tokens"
		}
		if len(estimate.ProxyLimits) != 1 || estimate.ProxyLimits[key] <= 0 {
			return estimate, BadAuthRequest("共享钱包代理请求须使用当前协议唯一的输出上限字段：" + key)
		}
		estimate.ExplicitOutputLimit = estimate.ProxyLimits[key]
	}
	capability, err := DecodeModelCapabilityConfig(item.CapabilityConfigJSON)
	if err != nil || capability == nil || capability.Text == nil || capability.Text.ContextWindowTokens <= 0 {
		return estimate, BadAuthRequest("外部钱包文本Token计费须配置模型上下文窗口上限")
	}
	limit := estimate.ExplicitOutputLimit
	if !estimate.Proxy {
		if capability != nil && capability.Text != nil && capability.Text.MaxOutputTokens > 0 {
			declared := int64(capability.Text.MaxOutputTokens)
			if limit <= 0 || declared < limit {
				limit = declared
			}
		}
	}
	if limit <= 0 || limit > int64(capability.Text.ContextWindowTokens) || limit > 1000000 {
		return estimate, BadAuthRequest("外部钱包要求请求具有有效的输出Token上限")
	}
	// Authorize the declared context ceiling, including image/video input tokens;
	// rune-count estimates cannot bound CJK or multimodal token usage.
	estimate.InputTokens = int64(capability.Text.ContextWindowTokens)
	estimate.ExternalBudget = true
	estimate.OutputTokens = limit
	estimate.ExplicitOutputLimit = limit
	return estimate, nil
}

func walletMultipleOutputs(body map[string]any) bool {
	for _, key := range []string{"n", "best_of"} {
		if value, ok := body[key]; ok && fmt.Sprint(value) != "1" {
			return true
		}
	}
	return false
}

func enforceWalletTextBody(body map[string]any, input canvasGenerationInput, wire string) error {
	if !input.EnforceWalletOutputLimit {
		return nil
	}
	if body == nil || input.MaxOutputTokens <= 0 || walletMultipleOutputs(body) {
		return errors.New("共享钱包请求缺少单输出授权或冻结Token上限")
	}
	for _, key := range []string{"max_tokens", "max_output_tokens", "max_completion_tokens"} {
		delete(body, key)
	}
	applyAgentOutputLimit(body, agentStepOutputLimit(input), wire)
	return nil
}
func (s *Service) AdminWalletSettings(actor *model.User) (*WalletSettingsView, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	cfg, err := s.repo.SharedWalletSettings()
	if err != nil {
		return nil, err
	}
	v := walletSettingsView(cfg)
	return &v, nil
}
func (s *Service) UpdateWalletSettings(ctx context.Context, actor *model.User, req WalletSettingsRequest) (*WalletSettingsView, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	current, err := s.repo.SharedWalletSettings()
	if err != nil {
		return nil, err
	}
	cfg := model.SharedWalletSettings{Mode: req.Mode, BaseURL: strings.TrimRight(strings.TrimSpace(req.BaseURL), "/"), ClientID: strings.TrimSpace(req.ClientID), SiteID: req.SiteID, Issuer: strings.TrimSpace(req.Issuer), RechargeURL: strings.TrimSpace(req.RechargeURL), SecretCipher: current.SecretCipher, Version: req.Version}
	if cfg.Mode != "internal" && cfg.Mode != "external" {
		return nil, BadAuthRequest("钱包模式无效")
	}
	if req.ClientSecret != "" {
		if len(req.ClientSecret) < 32 || len(req.ClientSecret) > 512 {
			return nil, BadAuthRequest("共享钱包密钥长度须为32至512字符")
		}
		cfg.SecretCipher, err = s.encryptSettingSecret(req.ClientSecret)
		if err != nil {
			return nil, err
		}
	}
	if cfg.Mode == "external" {
		if err = validateWalletSettings(cfg); err != nil {
			return nil, err
		}
		// Verify credentials against an already bound SSO member. Enabling the mode
		// never invents or links an account from a nickname/email.
		member, findErr := s.repo.SharedWalletProbeMember(cfg)
		if findErr != nil {
			if errors.Is(findErr, repository.ErrSharedWalletProbeMemberNotFound) {
				return nil, BadAuthRequest("未找到匹配的小洞AI登录绑定，请核对站点ID和SSO身份签发方（包括末尾斜杠）与免登配置完全一致，并至少完成一次该站点的会员免登")
			}
			return nil, findErr
		}
		if _, err = s.walletRequest(ctx, cfg, "balance", map[string]any{"site_id": cfg.SiteID, "member_id": member}); err != nil {
			return nil, err
		}
	}
	if err = s.repo.SaveSharedWalletSettings(cfg, req.Version); err != nil {
		return nil, BadAuthRequest(err.Error())
	}
	cfg.Version = req.Version + 1
	if err = s.appendAdminAudit(actor, "wallet.mode.update", "system_setting", model.SharedWalletSettingKey, "更新共享钱包配置", map[string]any{"mode": cfg.Mode, "version": cfg.Version}); err != nil {
		return nil, err
	}
	v := walletSettingsView(cfg)
	return &v, nil
}

func validateWalletSettings(cfg model.SharedWalletSettings) error {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Path != "" {
		return BadAuthRequest("钱包地址必须为不带路径的站点origin")
	}
	if base.Scheme != "https" && !(base.Scheme == "http" && (base.Hostname() == "127.0.0.1" || base.Hostname() == "localhost" || base.Hostname() == "::1")) {
		return BadAuthRequest("共享钱包必须使用HTTPS")
	}
	if cfg.SiteID <= 0 || cfg.ClientID == "" || len(cfg.ClientID) > 128 || cfg.SecretCipher == "" || cfg.Issuer == "" {
		return BadAuthRequest("共享钱包配置不完整")
	}
	if cfg.RechargeURL != "" {
		recharge, e := url.Parse(cfg.RechargeURL)
		if e != nil || recharge.Scheme != "https" || recharge.Host == "" || recharge.User != nil {
			return BadAuthRequest("充值链接必须为HTTPS地址")
		}
	}
	return nil
}

type walletReply struct {
	OK       bool   `json:"ok"`
	Reason   string `json:"reason"`
	Balance  int64  `json:"balance"`
	TradeNo  string `json:"trade_no"`
	Status   string `json:"status"`
	Reserved int64  `json:"reserved_point"`
	Charged  int64  `json:"charged_point"`
	Refunded int64  `json:"refunded_point"`
}
type walletBusinessError struct{ Reason string }

func (e *walletBusinessError) Error() string {
	if e.Reason == "insufficient" {
		return externalCreditsFailureMessage
	}
	return "共享钱包拒绝操作：" + e.Reason
}

// 同步模型代理通过 AppError 投影公开原因；保留外层类型与 Reason 供核账状态机判断。
func (e *walletBusinessError) Unwrap() error {
	if e.Reason == "insufficient" {
		return QuotaExceeded(externalCreditsFailureMessage)
	}
	return nil
}

func walletSignature(secret, path, ts, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("POST\n" + path + "\n" + ts + "\n" + nonce + "\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Service) walletRequest(ctx context.Context, cfg model.SharedWalletSettings, action string, payload any) (walletReply, error) {
	var result walletReply
	if err := validateWalletSettings(cfg); err != nil {
		return result, err
	}
	secret, err := s.decryptSettingSecret(cfg.SecretCipher)
	if err != nil {
		return result, errors.New("共享钱包凭证无法解密")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return result, err
	}
	path := "/api/ht_hot/billing/v1/" + action
	target := cfg.BaseURL + path
	if _, err = outbound.ValidateOutboundURL(target); err != nil {
		return result, errors.New("共享钱包地址未通过出站安全校验")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return result, err
	}
	nonceBytes := make([]byte, 24)
	if _, err = rand.Read(nonceBytes); err != nil {
		return result, err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := hex.EncodeToString(nonceBytes)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Wallet-Client", cfg.ClientID)
	request.Header.Set("X-Wallet-Ts", ts)
	request.Header.Set("X-Wallet-Nonce", nonce)
	request.Header.Set("X-Wallet-Sign", walletSignature(secret, path, ts, nonce, raw))
	client := outbound.OutboundHTTPClient(12 * time.Second)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("共享钱包不允许重定向") }
	response, err := client.Do(request)
	if err != nil {
		return result, errors.New("共享钱包暂不可用，请稍后核对原订单")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(body) > 65536 {
		return result, errors.New("共享钱包响应无效")
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &envelope) != nil || envelope.Code != 1 || json.Unmarshal(envelope.Data, &result) != nil {
		return result, errors.New("共享钱包服务拒绝请求或响应无效")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(envelope.Data, &fields) != nil || (result.OK && fields["balance"] == nil) || result.Balance < 0 || result.Balance > 9223372036854 {
		return result, errors.New("共享钱包余额响应无效")
	}
	if !result.OK {
		switch result.Reason {
		case "insufficient", "conflict", "not_found", "invalid_state", "member_unavailable", "invalid_request":
			return result, &walletBusinessError{Reason: result.Reason}
		}
		return result, errors.New("共享钱包返回未知拒绝原因")
	}
	return result, nil
}

func (s *Service) externalWalletSummary(user *model.User, cfg model.SharedWalletSettings, entryType string, page, limit int) (*WalletSummary, error) {
	member, err := s.repo.SharedWalletMember(user.ID, cfg)
	if err != nil {
		return nil, err
	}
	reply, err := s.walletRequest(context.Background(), cfg, "balance", map[string]any{"site_id": cfg.SiteID, "member_id": member})
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 30
	}
	entries, total, err := s.repo.ExternalWalletLedger(user.ID, entryType, limit, (page-1)*limit)
	if err != nil {
		return nil, err
	}
	return &WalletSummary{WalletMode: "external", RechargeURL: cfg.RechargeURL, Account: model.CreditAccount{UserID: user.ID, AvailableMicrocredits: reply.Balance * CreditScale}, Entries: entries, Total: total, Page: page, Limit: limit}, nil
}

// embedded Repository keeps read/uncertain behavior unchanged; only lifecycle
// methods dispatch by the immutable wallet source stored on each bill.
type walletBillingRepository struct {
	*repository.Repository
	service *Service
}

func (r walletBillingRepository) external(id string, action string, duration *int64) (bool, error) {
	order, err := r.BillingOrder(id)
	if err != nil {
		return false, err
	}
	if order.WalletMode != "external" {
		return false, nil
	}
	_, err = r.QueueWalletOperation(id, action, duration)
	if err != nil {
		if action == "settle" {
			_ = r.MarkBillingUncertain(id, "共享钱包实际费用待核对："+err.Error())
		}
		return true, err
	}
	if err = r.service.processWalletOrder(context.Background(), id); err != nil {
		return true, err
	}
	ops, err := r.WalletOperations(id)
	if err != nil {
		return true, err
	}
	for _, op := range ops {
		if op.Action == action && op.State != "succeeded" {
			return true, errors.New("共享钱包操作尚未成功，请核对原订单")
		}
	}
	return true, nil
}
func (r walletBillingRepository) MarkBillingRunning(id string) error {
	external, err := r.external(id, "reserve", nil)
	if err != nil {
		return err
	}
	if external {
		return r.MarkExternalWalletRunning(id)
	}
	return r.Repository.MarkBillingRunning(id)
}
func (r walletBillingRepository) SettleBillingOrder(id, providerID string) error {
	external, err := r.external(id, "settle", nil)
	if external || err != nil {
		return err
	}
	return r.Repository.SettleBillingOrder(id, providerID)
}
func (r walletBillingRepository) SettleBillingOrderWithAudioDuration(id, providerID string, duration int64) error {
	external, err := r.external(id, "settle", &duration)
	if external || err != nil {
		return err
	}
	return r.Repository.SettleBillingOrderWithAudioDuration(id, providerID, duration)
}
func (r walletBillingRepository) RefundBillingOrder(id, note string) error {
	external, err := r.external(id, "refund", nil)
	if external || err != nil {
		return err
	}
	return r.Repository.RefundBillingOrder(id, note)
}
func (r walletBillingRepository) RestoreRefundedBillingOrder(id, providerID string) error {
	external, err := r.external(id, "restore", nil)
	if external || err != nil {
		return err
	}
	return r.Repository.RestoreRefundedBillingOrder(id, providerID)
}

func (r walletBillingRepository) MarkBillingUncertain(id, reason string) error {
	order, err := r.BillingOrder(id)
	if err != nil {
		return err
	}
	// Recovery callers mark every settlement error uncertain. A definitive
	// insufficient restore changed no funds and must remain recoverable.
	if order.WalletMode == "external" && order.Status == model.BillingStatusRefunded && order.WalletState == "rejected" {
		return nil
	}
	return r.Repository.MarkBillingUncertain(id, reason)
}

func (s *Service) processWalletOrder(ctx context.Context, id string) error {
	token := newID()
	claimed, err := s.repo.ClaimWalletOrder(id, token)
	if err != nil {
		return err
	}
	if !claimed {
		return errors.New("共享钱包请求正在处理，请稍后对账")
	}
	defer s.repo.ReleaseWalletOrder(id, token)
	order, err := s.repo.BillingOrder(id)
	if err != nil {
		return err
	}
	var cfg model.SharedWalletSettings
	if err = json.Unmarshal([]byte(order.WalletConfigJSON), &cfg); err != nil {
		return errors.New("原钱包配置快照损坏")
	}
	ops, err := s.repo.WalletOperations(id)
	if err != nil {
		return err
	}
	reserveRejected := false
	for _, op := range ops {
		if op.Action == "reserve" && op.State == "rejected" {
			reserveRejected = true
		}
		if op.State != "pending" {
			continue
		}
		if op.Action == "refund" && reserveRejected {
			if err = s.repo.CompleteWalletOperation(op, "succeeded", `{"local":true}`, 0, 0, ""); err != nil {
				return err
			}
			continue
		}
		payload := map[string]any{"site_id": cfg.SiteID, "member_id": order.WalletMemberID, "trade_no": order.ID, "operation_id": op.ID, "method_key": order.Model, "memo": "Canvas " + order.Scene}
		if op.Action != "refund" {
			payload["point"] = op.Point
		}
		if op.Attempts > 0 {
			// Status is advisory: replaying the original immutable operation is
			// still necessary because a trade status cannot prove which request ran.
			_, statusErr := s.walletRequest(ctx, cfg, "status", map[string]any{"site_id": cfg.SiteID, "member_id": order.WalletMemberID, "trade_no": order.ID})
			var business *walletBusinessError
			if statusErr != nil && !(errors.As(statusErr, &business) && business.Reason == "not_found") {
				return statusErr
			}
		}
		if err = s.repo.AttemptWalletOperation(op.ID); err != nil {
			return err
		}
		reply, requestErr := s.walletRequest(ctx, cfg, op.Action, payload)
		if requestErr != nil {
			var rejected *walletBusinessError
			if errors.As(requestErr, &rejected) && (op.Action != "reserve" || rejected.Reason == "insufficient" || rejected.Reason == "member_unavailable") {
				raw, _ := json.Marshal(reply)
				if err = s.repo.CompleteWalletOperation(op, "rejected", string(raw), reply.Balance, reply.Charged, rejected.Reason); err != nil {
					return err
				}
			} else {
				_ = s.repo.FailWalletOperation(op.ID, "remote_unconfirmed")
				_ = s.repo.MarkBillingUncertain(id, "共享钱包响应未确认，需按原请求对账")
			}
			return requestErr
		}
		expected := map[string]string{"reserve": "reserved", "settle": "settled", "refund": "refunded", "restore": "settled"}[op.Action]
		if reply.TradeNo != order.ID || reply.Status != expected || reply.Charged < 0 || reply.Charged > reply.Reserved || (op.Action != "refund" && reply.Charged != op.Point) || (op.Action == "refund" && reply.Charged != 0) {
			_ = s.repo.FailWalletOperation(op.ID, "response_mismatch")
			_ = s.repo.MarkBillingUncertain(id, "共享钱包订单响应不匹配，需核对原单")
			return errors.New("共享钱包订单响应不匹配，请核对原单")
		}
		raw, _ := json.Marshal(reply)
		if err = s.repo.CompleteWalletOperation(op, "succeeded", string(raw), reply.Balance, reply.Charged, ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ReconcileSharedWallet(ctx context.Context, actor *model.User) (int, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return 0, err
	}
	ids, err := s.repo.RejectedWalletRestores()
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err = s.repo.QueueWalletOperation(id, "restore", nil); err != nil {
			return 0, err
		}
	}
	return s.reconcileSharedWallet(ctx)
}
func (s *Service) reconcileSharedWallet(ctx context.Context) (int, error) {
	ids, err := s.repo.PendingWalletOrders()
	if err != nil {
		return 0, err
	}
	done := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			return done, ctx.Err()
		}
		if err = s.processWalletOrder(ctx, id); err == nil {
			done++
		}
	}
	return done, nil
}
func (s *Service) startSharedWalletWorker(ctx context.Context) {
	s.runWorkerLoop(func(ctx context.Context) {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.reconcileSharedWallet(ctx)
			}
		}
	})
}
