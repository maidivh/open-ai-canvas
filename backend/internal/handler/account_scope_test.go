package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/integrations/cloooud"
)

func TestAccountScopeStopsStaleTabWrites(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, expected string
		guard, rejected              bool
	}{
		{"old tab", "POST", "/api/projects", "old-user", true, true},
		{"old client without header", "POST", "/api/projects", "", true, true},
		{"new tab", "POST", "/api/projects", "new-user", true, false},
		{"old tab logout", "POST", "/api/auth/logout", "old-user", true, true},
		{"bootstrap discovers new user", "GET", "/api/auth/session", "old-user", true, false},
		{"media without custom header", "GET", "/api/resources/file", "", true, false},
		{"stale read", "GET", "/api/projects", "old-user", true, true},
		{"native client", "POST", "/api/projects", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(tc.method, tc.path, nil)
			c.Request.Header.Set("X-Canvas-User-ID", tc.expected)
			if tc.guard {
				c.Request.AddCookie(&http.Cookie{Name: cloooud.AccountScopeCookie, Value: "1"})
			}
			if err := validateAccountScope(c, "new-user"); (err != nil) != tc.rejected {
				t.Fatalf("rejected=%v, error=%v", tc.rejected, err)
			}
		})
	}
}
