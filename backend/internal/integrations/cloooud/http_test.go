package cloooud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	nativeauth "yingce/backend/internal/auth"
	"yingce/backend/internal/kernel"
)

type cloooudTestAuthenticator struct {
	calls                                int
	failure                              bool
	state, code, browser, currentSession string
}

func (s *cloooudTestAuthenticator) BeginCloooudLogin(string) (*CloooudLoginStart, error) {
	return &CloooudLoginStart{URL: "https://identity.example/ai/canvas/authorize", BrowserState: strings.Repeat("a", 64)}, nil
}
func (s *cloooudTestAuthenticator) CompleteCloooudLogin(_ context.Context, state, code, browser, currentSession string) (*CloooudCallbackResult, error) {
	s.calls++
	s.state = state
	s.code = code
	s.browser = browser
	s.currentSession = currentSession
	if s.failure {
		return nil, errors.New("private-diagnostic-sentinel")
	}
	return &CloooudCallbackResult{Session: &nativeauth.AuthSessionResult{Session: "session.secret", MaxAgeSecs: 3600}, Next: "/create"}, nil
}

func newCloooudRouter(t *testing.T, auth *cloooudTestAuthenticator) *gin.Engine {
	t.Helper()
	r := gin.New()
	RegisterRoutes(r.Group("/api"), auth, HTTPHost{
		EnforceRateLimit: func(*gin.Context, string, int, time.Duration) bool { return true },
		Fail: func(c *gin.Context, err error) {
			var appErr *kernel.AppError
			if !errors.As(err, &appErr) {
				t.Fatal(err)
			}
			c.JSON(appErr.Status, gin.H{"code": appErr.Code, "msg": appErr.Message})
		},
		SessionCookie: func(c *gin.Context) string { value, _ := c.Cookie(nativeauth.SessionCookieName); return value },
		SetSessionCookie: func(c *gin.Context, value string, maxAge int) {
			http.SetCookie(c.Writer, &http.Cookie{Name: nativeauth.SessionCookieName, Value: value, Path: "/", HttpOnly: true, Secure: true, MaxAge: maxAge, SameSite: http.SameSiteLaxMode})
		},
	})
	return r
}

func TestCloooudHandlersSetHostOnlyCookiesAndRedirect(t *testing.T) {
	auth := &cloooudTestAuthenticator{}
	r := newCloooudRouter(t, auth)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "https://canvas.example/api/auth/cloooud/start", nil)
	r.ServeHTTP(w, req)
	if w.Code != 302 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("start response: %d %v", w.Code, w.Header())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Domain != "" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/api/auth/cloooud" {
		t.Fatalf("insecure state cookie: %#v", cookies)
	}
	req = httptest.NewRequest("GET", "https://canvas.example/api/auth/cloooud/callback?state="+strings.Repeat("a", 64)+"&code="+strings.Repeat("c", 64), nil)
	req.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 302 || w.Header().Get("Location") != "/create" || auth.browser != strings.Repeat("a", 64) {
		t.Fatal("callback failed")
	}
	var sessionCookie, clearedState, guardedWrites bool
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == nativeauth.SessionCookieName {
			sessionCookie = cookie.HttpOnly && cookie.Secure && cookie.Domain == "" && cookie.Value == "session.secret"
		}
		if cookie.Name == cloooudStateCookie {
			clearedState = cookie.MaxAge < 0
		}
		if cookie.Name == AccountScopeCookie {
			guardedWrites = cookie.Value == "1" && cookie.HttpOnly && cookie.Secure && cookie.Path == "/" && cookie.MaxAge > 0
		}
	}
	if !sessionCookie || !clearedState || !guardedWrites {
		t.Fatal("callback did not finish cookie lifecycle")
	}
}

func TestCloooudCallbackKeepsExistingAccountFailureVisible(t *testing.T) {
	auth := &cloooudTestAuthenticator{failure: true}
	r := newCloooudRouter(t, auth)
	req := httptest.NewRequest("GET", "https://canvas.example/api/auth/cloooud/callback?state="+strings.Repeat("a", 64)+"&code="+strings.Repeat("c", 64), nil)
	req.AddCookie(&http.Cookie{Name: nativeauth.SessionCookieName, Value: "original.session"})
	req.AddCookie(&http.Cookie{Name: cloooudStateCookie, Value: strings.Repeat("a", 64)})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || w.Header().Get("Location") != "" || auth.currentSession != "original.session" || !strings.Contains(w.Body.String(), "原登录状态已保留") || strings.Contains(w.Body.String(), "private-diagnostic-sentinel") {
		t.Fatalf("account conflict must stay visible: %d %s", w.Code, w.Body.String())
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == nativeauth.SessionCookieName {
			t.Fatal("failure replaced original session")
		}
	}
}

func TestCloooudCallbackRejectsDuplicateQueryAndHidesFailure(t *testing.T) {
	auth := &cloooudTestAuthenticator{failure: true}
	r := newCloooudRouter(t, auth)
	for _, query := range []string{"state=a&state=b&code=c", "state=a&code=b&code=c", "state=%zz&code=c"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "https://canvas.example/api/auth/cloooud/callback?"+query, nil))
		if auth.calls != 0 || w.Code != 302 {
			t.Fatal("ambiguous query reached authentication")
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "https://canvas.example/api/auth/cloooud/callback?state=a&code=c", nil))
	if strings.Contains(w.Header().Get("Location"), "private-diagnostic") || !strings.HasPrefix(w.Header().Get("Location"), "/login?oauth_error=") {
		t.Fatal("unsafe failure response")
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == nativeauth.SessionCookieName {
			t.Fatal("failure issued login cookie")
		}
	}
}
