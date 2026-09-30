package cloooud

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"

	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
)

const cloooudProvider = "cloooud"

// Service 持有启动入口传入的数据库与宿主能力，不依赖 app/service 组合根。
type Service struct {
	db   *gorm.DB
	repo *repository.Repository
	auth NativeHost
}

func New(db *gorm.DB, host NativeHost) *Service {
	return &Service{db: db, repo: repository.New(db), auth: host}
}

var cloooudCodePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type CloooudLoginStart struct {
	URL          string
	BrowserState string
}

type CloooudCallbackResult struct {
	Session *auth.AuthSessionResult
	Next    string
}

type cloooudProfile struct {
	Issuer      string `json:"issuer"`
	SiteID      int64  `json:"site_id"`
	MemberID    int64  `json:"member_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

func (s *Service) BeginCloooudLogin(nextPath string) (*CloooudLoginStart, error) {
	cfg, err := readCloooudConfig()
	if err != nil {
		return nil, err
	}
	count, err := s.repo.UserCount()
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, kernel.Forbidden("请先创建影策本地管理员账号，再启用小洞单点登录")
	}
	var random [64]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, errors.New("无法创建登录请求，请稍后重试")
	}
	state, verifier := hex.EncodeToString(random[:32]), hex.EncodeToString(random[32:])
	if err := s.repo.CreateOAuthState(&model.OAuthState{
		ID: kernel.NewID(), Provider: cloooudProvider, StateHash: auth.HashToken(state), CodeVerifier: verifier,
		NextPath: safeCloooudNext(nextPath), ExpiresAt: time.Now().Add(5 * time.Minute),
	}); err != nil {
		return nil, err
	}
	challenge := sha256.Sum256([]byte(verifier))
	target, _ := url.Parse(cfg.AuthorizationURL)
	target.RawQuery = url.Values{
		"client_id": {cfg.ClientID}, "response_type": {"code"}, "redirect_uri": {cfg.RedirectURL}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"},
	}.Encode()
	return &CloooudLoginStart{URL: target.String(), BrowserState: state}, nil
}

func (s *Service) CompleteCloooudLogin(ctx context.Context, stateValue, code, browserState, currentSession string) (*CloooudCallbackResult, error) {
	// 先绑定发起登录的浏览器，再消费一次性 state，防止其他浏览器注入登录结果。
	if !cloooudCodePattern.MatchString(stateValue) || !cloooudCodePattern.MatchString(code) ||
		subtle.ConstantTimeCompare([]byte(stateValue), []byte(browserState)) != 1 {
		return nil, kernel.BadAuthRequest("登录请求已失效，请从小洞重新打开画布/短剧")
	}
	cfg, err := readCloooudConfig()
	if err != nil {
		return nil, err
	}
	state, err := s.repo.ConsumeOAuthState(cloooudProvider, auth.HashToken(stateValue))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kernel.BadAuthRequest("登录请求已过期或已使用，请重新进入")
	}
	if err != nil {
		return nil, err
	}
	profile, err := exchangeCloooudCode(ctx, cfg, code, state.CodeVerifier)
	if err != nil {
		return nil, err
	}
	// Cookie 在标签页间共享，旧画布可能仍保留原账号的数据，不能在后台替换为另一身份。
	if currentSession != "" {
		current, err := s.auth.CurrentUser(currentSession)
		if err != nil {
			return nil, kernel.Forbidden("请关闭其他影策标签页并退出原登录后重新进入")
		}
		identity, err := s.repo.UserIdentity(cloooudProvider, cloooudSubject(profile.Issuer, profile.SiteID, profile.MemberID))
		if err != nil || identity.UserID != current.ID {
			return nil, kernel.Forbidden("影策当前登录的是其他账号，请关闭其他影策标签页并退出原登录后重新进入")
		}
	}
	user, err := s.cloooudUser(cfg, profile)
	if err != nil {
		return nil, err
	}
	if user.Status != model.UserStatusActive {
		return nil, kernel.Forbidden("该影策账号已被禁用")
	}
	session, err := s.completeNativeLogin(user.ID)
	if err != nil {
		return nil, err
	}
	return &CloooudCallbackResult{Session: session, Next: safeCloooudNext(state.NextPath)}, nil
}

func (s *Service) cloooudUser(cfg cloooudConfig, profile cloooudProfile) (*model.User, error) {
	subject := cloooudSubject(profile.Issuer, profile.SiteID, profile.MemberID)
	identity, err := s.repo.UserIdentity(cloooudProvider, subject)
	if err == nil {
		return s.repo.User(identity.UserID)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if !cfg.AutoRegister {
		return nil, kernel.Forbidden("此小洞账号尚未关联影策，请联系管理员")
	}
	// 只认来源、站点和会员 ID，不通过邮箱、手机号、昵称合并已有账号。
	user := &model.User{ID: kernel.NewID(), Username: "cloooud_" + auth.HashToken(subject)[:24], DisplayName: auth.NormalizeDisplayName(profile.DisplayName, "小洞会员"), Role: model.UserRoleUser, Status: model.UserStatusActive}
	identity = &model.UserIdentity{ID: kernel.NewID(), UserID: user.ID, Provider: cloooudProvider, Subject: subject, ProviderUsername: kernel.TruncateRunes(profile.Username, 160)}
	if err := s.repo.CreateOAuthUser(user, identity); err != nil {
		// 并发首次登录可能命中唯一约束，只能复用完全相同的外部身份。
		if existing, findErr := s.repo.UserIdentity(cloooudProvider, subject); findErr == nil {
			return s.repo.User(existing.UserID)
		}
		return nil, err
	}
	return user, nil
}

func cloooudSubject(issuer string, siteID, memberID int64) string {
	return fmt.Sprintf("%s:%d:%d", auth.HashToken(issuer), siteID, memberID)
}
