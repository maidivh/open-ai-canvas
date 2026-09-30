package cloooud_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/handler"
	"infinite-canvas/backend/internal/integrations/cloooud"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/service"
)

// 覆盖真实 handler → app → 独立模块 → 原生 auth 的接入链，不连接业务数据库。
func TestHostRoutesCreateNativeSession(t *testing.T) {
	var challenge string
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || r.Form.Get("client_secret") != strings.Repeat("s", 32) {
			t.Error("host lost PKCE or client credentials")
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "data": map[string]any{"issuer": "https://identity.example", "site_id": 1, "member_id": 42, "display_name": "Member"}})
	}))
	defer issuer.Close()
	for key, value := range map[string]string{"ENABLED": "true", "AUTO_REGISTER": "true", "CLIENT_ID": "canvas", "CLIENT_SECRET": strings.Repeat("s", 32), "ISSUER": "https://identity.example", "SITE_ID": "1", "AUTHORIZATION_URL": "https://design.example/canvas/authorize", "EXCHANGE_URL": issuer.URL, "REDIRECT_URL": "https://canvas.example/api/auth/cloooud/callback"} {
		t.Setenv("CANVAS_CLOOOUD_SSO_"+key, value)
	}
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("REDIS_URL", "")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.User{}, &model.UserIdentity{}, &model.OAuthState{}, &model.AuthSession{}, &model.CreditAccount{}, &model.CreditLedgerEntry{}, &model.SystemSetting{}, &model.UserDailyActivity{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: "owner", Username: "owner", Role: model.UserRoleAdmin, Status: model.UserStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.New(db), t.TempDir())
	handler.ConfigureRuntime(svc)
	t.Cleanup(func() { handler.ConfigureRuntime(nil) })
	router := gin.New()
	api := router.Group("/api")
	handler.RegisterCanvasAPI(api, svc)
	cloooud.RegisterRoutes(api, cloooud.New(db, svc.CloooudHost()), handler.CloooudHTTPHost())
	start := httptest.NewRecorder()
	router.ServeHTTP(start, httptest.NewRequest("GET", "https://canvas.example/api/auth/cloooud/start", nil))
	if start.Code != 302 {
		t.Fatalf("start: %d %s", start.Code, start.Body.String())
	}
	authorization, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	challenge = authorization.Query().Get("code_challenge")
	callback := httptest.NewRequest("GET", "https://canvas.example/api/auth/cloooud/callback?state="+authorization.Query().Get("state")+"&code="+strings.Repeat("c", 64), nil)
	for _, cookie := range start.Result().Cookies() {
		callback.AddCookie(cookie)
	}
	result := httptest.NewRecorder()
	router.ServeHTTP(result, callback)
	if result.Code != 302 || result.Header().Get("Location") != "/create" {
		t.Fatalf("callback: %d %s", result.Code, result.Body.String())
	}
	var cookie *http.Cookie
	for _, item := range result.Result().Cookies() {
		if item.Name == service.SessionCookieName {
			cookie = item
		}
	}
	if cookie == nil || !cookie.HttpOnly || !cookie.Secure || cookie.Domain != "" {
		t.Fatal("native secure session cookie missing")
	}
	user, err := svc.CurrentUser(cookie.Value)
	if err != nil || user.ID == "owner" || user.Role != model.UserRoleUser {
		t.Fatalf("native session rejected: %v", err)
	}
	if err := svc.Logout(cookie.Value); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CurrentUser(cookie.Value); err == nil {
		t.Fatal("native logout did not revoke SSO session")
	}
	spec := httptest.NewRecorder()
	router.ServeHTTP(spec, httptest.NewRequest("GET", "https://canvas.example/api/openapi.yaml", nil))
	if spec.Code != 200 || strings.Count(spec.Body.String(), "/auth/cloooud/callback:") != 1 {
		t.Fatal("host did not expose module API contract")
	}
}
