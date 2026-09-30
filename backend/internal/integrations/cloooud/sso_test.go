package cloooud

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func configureCloooudTest(t *testing.T, endpoint string) {
	t.Helper()
	for key, value := range map[string]string{
		"ENABLED": "true", "AUTO_REGISTER": "true", "CLIENT_ID": "canvas", "CLIENT_SECRET": strings.Repeat("s", 32),
		"ISSUER": "https://identity.example", "SITE_ID": "1", "AUTHORIZATION_URL": "https://identity.example/ai/canvas/authorize",
		"EXCHANGE_URL": endpoint, "REDIRECT_URL": "https://canvas.example/api/auth/cloooud/callback",
	} {
		t.Setenv("CANVAS_CLOOOUD_SSO_"+key, value)
	}
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
}

func TestCloooudLoginBindsBrowserAndReusesIdentity(t *testing.T) {
	var challenge string
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("client_secret") != strings.Repeat("s", 32) || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			t.Error("exchange must authenticate client and prove PKCE verifier")
		}
		if r.Method != "POST" || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != "https://canvas.example/api/auth/cloooud/callback" || r.Header.Get("Site-id") != "1" {
			t.Error("exchange contract mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "data": map[string]any{
			"issuer": "https://identity.example", "site_id": 1, "member_id": 42, "username": "member42", "display_name": "小洞会员",
			"email": "owner@example.com", "point": 9000, "role": "admin",
		}})
	}))
	defer server.Close()
	configureCloooudTest(t, server.URL)
	svc, db := newCloooudTestService(t)
	if err := db.Create(&model.User{ID: "admin", Username: "member42", Email: "owner@example.com", Role: model.UserRoleAdmin, Status: model.UserStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	var userID string
	var currentSession string
	for i := 0; i < 2; i++ {
		start, err := svc.BeginCloooudLogin("/projects")
		if err != nil {
			t.Fatal(err)
		}
		target, _ := url.Parse(start.URL)
		state := target.Query().Get("state")
		challenge = target.Query().Get("code_challenge")
		if len(state) != 64 || len(challenge) != 43 || target.Query().Get("code_challenge_method") != "S256" {
			t.Fatal("missing authorization binding")
		}
		var stored model.OAuthState
		if err := db.Where("provider = ? AND state_hash = ?", "cloooud", auth.HashToken(state)).First(&stored).Error; err != nil {
			t.Fatal(err)
		}
		if stored.StateHash == state || strings.Contains(start.URL, stored.CodeVerifier) {
			t.Fatal("authorization secrets exposed")
		}
		if _, err := svc.CompleteCloooudLogin(context.Background(), state, strings.Repeat("c", 64), "wrong-browser", currentSession); err == nil {
			t.Fatal("accepted another browser's callback")
		}
		if calls != i {
			t.Fatal("browser mismatch must not exchange or consume the valid state")
		}
		result, err := svc.CompleteCloooudLogin(context.Background(), state, strings.Repeat("c", 64), start.BrowserState, currentSession)
		if err != nil {
			t.Fatal(err)
		}
		if result.Next != "/projects" || result.Session.User.Role != model.UserRoleUser {
			t.Fatalf("unexpected login: %#v", result)
		}
		if result.Session.User.ID == "admin" || result.Session.User.Email != "" || result.Session.User.PasswordHash != "" {
			t.Fatal("SSO merged existing credentials")
		}
		if i == 0 {
			userID = result.Session.User.ID
		} else if result.Session.User.ID != userID {
			t.Fatal("repeat login created another user")
		}
		if _, err := svc.auth.CurrentUser(result.Session.Session); err != nil {
			t.Fatal(err)
		}
		currentSession = result.Session.Session
		if _, err := svc.CompleteCloooudLogin(context.Background(), state, strings.Repeat("c", 64), start.BrowserState, currentSession); err == nil {
			t.Fatal("accepted replay")
		}
	}
	var account model.CreditAccount
	if err := db.First(&account, "user_id = ?", userID).Error; err != nil {
		t.Fatal(err)
	}
	if account.AvailableMicrocredits != 0 {
		t.Fatal("SSO imported upstream credits")
	}
	var count int64
	if err := db.Model(&model.UserIdentity{}).Where("provider = ?", "cloooud").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("identity count=%d err=%v", count, err)
	}
}

func TestCloooudRefusesReplacingExistingBrowserAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "data": cloooudProfile{Issuer: "https://identity.example", SiteID: 1, MemberID: 42}})
	}))
	defer server.Close()
	configureCloooudTest(t, server.URL)
	svc, db := newCloooudTestService(t)
	admin := &model.User{ID: "admin", Username: "admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive}
	if err := db.Create(admin).Error; err != nil {
		t.Fatal(err)
	}
	session, err := svc.completeNativeLogin(admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range []string{session.Session, "invalid.stale-session"} {
		start, err := svc.BeginCloooudLogin("/create")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = svc.CompleteCloooudLogin(context.Background(), start.BrowserState, strings.Repeat("c", 64), start.BrowserState, cookie); err == nil {
			t.Fatal("SSO replaced an existing or unverifiable browser account")
		}
	}
	other := &model.User{ID: "other", Username: "other", Role: model.UserRoleUser, Status: model.UserStatusActive}
	identity := &model.UserIdentity{ID: "other-identity", UserID: other.ID, Provider: cloooudProvider, Subject: cloooudSubject("https://identity.example", 1, 42)}
	if err := svc.repo.CreateOAuthUser(other, identity); err != nil {
		t.Fatal(err)
	}
	start, err := svc.BeginCloooudLogin("/create")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteCloooudLogin(context.Background(), start.BrowserState, strings.Repeat("c", 64), start.BrowserState, session.Session); err == nil {
		t.Fatal("SSO switched to an existing different mapped member")
	}
	var users, sessions int64
	db.Model(&model.User{}).Count(&users)
	db.Model(&model.AuthSession{}).Count(&sessions)
	if users != 2 || sessions != 1 {
		t.Fatalf("failed switch wrote users/sessions: %d/%d", users, sessions)
	}
	if user, err := svc.auth.CurrentUser(session.Session); err != nil || user.ID != admin.ID {
		t.Fatal("failed switch invalidated original account")
	}
}

func TestCloooudRejectsInvalidLoginWithoutSession(t *testing.T) {
	for _, scenario := range []string{"expired", "tenant", "issuer", "missing-member", "disabled", "provisioning-disabled", "upstream-error"} {
		t.Run(scenario, func(t *testing.T) {
			profile := map[string]any{"issuer": "https://identity.example", "site_id": 1, "member_id": 42, "display_name": "Member"}
			if scenario == "tenant" {
				profile["site_id"] = 2
			}
			if scenario == "issuer" {
				profile["issuer"] = "https://other.example"
			}
			if scenario == "missing-member" {
				profile["member_id"] = 0
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "upstream-error" {
					w.WriteHeader(401)
					_, _ = w.Write([]byte("secret-sentinel"))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "data": profile})
			}))
			defer server.Close()
			configureCloooudTest(t, server.URL)
			svc, db := newCloooudTestService(t)
			if err := db.Create(&model.User{ID: "admin", Username: "admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive}).Error; err != nil {
				t.Fatal(err)
			}
			if scenario == "disabled" {
				if err := db.Create(&model.User{ID: "blocked", Username: "blocked", Status: model.UserStatusDisabled}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&model.UserIdentity{ID: "id", UserID: "blocked", Provider: "cloooud", Subject: cloooudSubject("https://identity.example", 1, 42)}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "provisioning-disabled" {
				t.Setenv("CANVAS_CLOOOUD_SSO_AUTO_REGISTER", "false")
			}
			start, err := svc.BeginCloooudLogin("//outside.example")
			if err != nil {
				t.Fatal(err)
			}
			target, _ := url.Parse(start.URL)
			if scenario == "expired" {
				if err := db.Model(&model.OAuthState{}).Where("provider = ?", "cloooud").Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
					t.Fatal(err)
				}
			}
			_, err = svc.CompleteCloooudLogin(context.Background(), target.Query().Get("state"), strings.Repeat("c", 64), start.BrowserState, "")
			if err == nil {
				t.Fatalf("accepted %s", scenario)
			}
			if strings.Contains(err.Error(), "secret-sentinel") {
				t.Fatal("upstream diagnostic leaked")
			}
			var count int64
			if err := db.Model(&model.AuthSession{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("invalid callback issued session: %d %v", count, err)
			}
		})
	}
}

func TestCloooudConfigAndFirstAdmin(t *testing.T) {
	configureCloooudTest(t, "https://identity.example/api/exchange")
	for key, value := range map[string]string{"AUTHORIZATION_URL": "https://user:secret@identity.example/authorize", "EXCHANGE_URL": "http://public.example/exchange", "REDIRECT_URL": "https://canvas.example/callback?next=bad", "CLIENT_ID": "invalid client", "CLIENT_SECRET": "", "SITE_ID": "0", "ENABLED": "false", "AUTO_REGISTER": "invalid"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("CANVAS_CLOOOUD_SSO_"+key, value)
			if _, err := readCloooudConfig(); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
	for _, raw := range []string{"https://:443", "https://identity.example/#", "https://identity.example/\\path"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("CANVAS_CLOOOUD_SSO_AUTHORIZATION_URL", raw)
			if _, err := readCloooudConfig(); err == nil {
				t.Fatal("accepted invalid URL")
			}
		})
	}
	t.Run("shared-secret-limit", func(t *testing.T) {
		t.Setenv("CANVAS_CLOOOUD_SSO_CLIENT_SECRET", strings.Repeat("s", 512))
		if _, err := readCloooudConfig(); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CANVAS_CLOOOUD_SSO_CLIENT_SECRET", strings.Repeat("s", 513))
		if _, err := readCloooudConfig(); err == nil {
			t.Fatal("accepted oversized secret")
		}
	})
	svc, _ := newCloooudTestService(t)
	if _, err := svc.BeginCloooudLogin("/create"); err == nil {
		t.Fatal("SSO cannot bootstrap installation")
	}
	if cloooudSubject("https://identity.example", 1, 42) == cloooudSubject("https://identity.example", 2, 42) || cloooudSubject("https://identity.example", 1, 42) == cloooudSubject("https://another.example", 1, 42) {
		t.Fatal("tenant identities collide")
	}
}

func TestCloooudExchangeNeverFollowsRedirects(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer server.Close()
	configureCloooudTest(t, server.URL)
	cfg, err := readCloooudConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exchangeCloooudCode(context.Background(), cfg, strings.Repeat("c", 64), strings.Repeat("v", 64)); err == nil || forwarded {
		t.Fatal("exchange followed redirect or accepted invalid response")
	}
}

func TestCloooudNextRejectsExternalAndAuthPaths(t *testing.T) {
	for _, raw := range []string{"", "//outside.example", "https://outside.example", "/\\outside.example", "/%5coutside.example", "/%2foutside.example", "/api/auth/cloooud/start", "/login", "/x\r\nLocation: https://outside.example"} {
		if got := safeCloooudNext(raw); got != "/create" {
			t.Errorf("next %q accepted as %q", raw, got)
		}
	}
	if got := safeCloooudNext("/projects?q=test"); got != "/projects?q=test" {
		t.Fatalf("safe path lost: %s", got)
	}
}

func TestNativeLoginStopsBeforeSessionOnPolicyFailureOrConcurrentDisable(t *testing.T) {
	for _, scenario := range []string{"policy-failure", "concurrent-disable"} {
		t.Run(scenario, func(t *testing.T) {
			svc, db := newCloooudTestService(t)
			user := &model.User{ID: "member", Username: "member", Role: model.UserRoleUser, Status: model.UserStatusActive}
			if err := db.Create(user).Error; err != nil {
				t.Fatal(err)
			}
			policyErr := errors.New("policy unavailable")
			svc.auth.EnsureSignupBonus = func(string) error {
				if scenario == "policy-failure" {
					return policyErr
				}
				return db.Model(&model.User{}).Where("id = ?", user.ID).Update("status", model.UserStatusDisabled).Error
			}
			activity := false
			svc.auth.RecordActivity = func(string, string, int) { activity = true }
			if _, err := svc.completeNativeLogin(user.ID); err == nil {
				t.Fatal("failed policy or disabled user obtained a session")
			}
			var sessions int64
			if err := db.Model(&model.AuthSession{}).Count(&sessions).Error; err != nil {
				t.Fatal(err)
			}
			if sessions != 0 || activity {
				t.Fatal("failed login created session or activity")
			}
			if scenario == "concurrent-disable" {
				if err := db.First(user, "id = ?", user.ID).Error; err != nil {
					t.Fatal(err)
				}
				if user.Status != model.UserStatusDisabled {
					t.Fatal("login overwrote administrator disable")
				}
			}
		})
	}
}

func newCloooudTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.UserIdentity{}, &model.OAuthState{}, &model.CreditAccount{}, &model.AuthSession{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := repository.New(db)
	native := auth.New(repo, nil, nil)
	return New(db, NativeHost{
		CurrentUser:       native.CurrentUser,
		CreateSession:     native.SessionIssuer(),
		EnsureSignupBonus: func(string) error { return nil },
		RecordActivity:    func(string, string, int) {},
	}), db
}
