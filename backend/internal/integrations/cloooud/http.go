package cloooud

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/kernel"
)

const cloooudStateCookie = "open_ai_canvas_cloooud_state"

// HTTPHost 保留宿主的错误信封、限流和会话 Cookie 策略。
type HTTPHost struct {
	EnforceRateLimit func(*gin.Context, string, int, time.Duration) bool
	Fail             func(*gin.Context, error)
	SessionCookie    func(*gin.Context) string
	SetSessionCookie func(*gin.Context, string, int)
}

type Authenticator interface {
	BeginCloooudLogin(string) (*CloooudLoginStart, error)
	CompleteCloooudLogin(context.Context, string, string, string, string) (*CloooudCallbackResult, error)
}

func RegisterRoutes(r *gin.RouterGroup, svc Authenticator, host HTTPHost) {
	r.GET("/auth/cloooud/start", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		if !host.EnforceRateLimit(c, "cloooud-start:"+c.ClientIP(), 20, 10*time.Minute) {
			return
		}
		query, err := url.ParseQuery(c.Request.URL.RawQuery)
		if err != nil || len(c.Request.URL.RawQuery) > 2048 || len(query["next"]) > 1 {
			host.Fail(c, kernel.BadAuthRequest("登录跳转参数无效"))
			return
		}
		result, err := svc.BeginCloooudLogin(query.Get("next"))
		if err != nil {
			host.Fail(c, err)
			return
		}
		setCloooudStateCookie(c, result.BrowserState, 300)
		c.Redirect(http.StatusFound, result.URL)
	})
	r.GET("/auth/cloooud/callback", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		if !host.EnforceRateLimit(c, "cloooud-callback:"+c.ClientIP(), 30, 10*time.Minute) {
			return
		}
		failureURL := "/login?oauth_error=" + url.QueryEscape("小洞单点登录未完成，请返回小洞重新打开画布/短剧；如仍失败请联系管理员")
		query, err := url.ParseQuery(c.Request.URL.RawQuery)
		if err != nil || len(c.Request.URL.RawQuery) > 2048 || len(query["state"]) != 1 || len(query["code"]) != 1 {
			c.Redirect(http.StatusFound, failureURL)
			return
		}
		browserState, _ := c.Cookie(cloooudStateCookie)
		// 无关或损坏的回调不清除其他正在进行的登录请求。
		if browserState != "" && subtle.ConstantTimeCompare([]byte(browserState), []byte(query.Get("state"))) == 1 {
			setCloooudStateCookie(c, "", -1)
		}
		currentSession := host.SessionCookie(c)
		result, err := svc.CompleteCloooudLogin(c.Request.Context(), query.Get("state"), query.Get("code"), browserState, currentSession)
		if err != nil {
			if currentSession != "" {
				// 登录页会将已有会话直接导航回工作区；保留明确失败，避免误以为已切换账号。
				host.Fail(c, kernel.Forbidden("小洞免登未完成。影策存在原登录状态；如需切换账号，请关闭其他影策标签页并退出原登录，再从小洞重新进入。"))
				return
			}
			c.Redirect(http.StatusFound, failureURL)
			return
		}
		host.SetSessionCookie(c, result.Session.Session, result.Session.MaxAgeSecs)
		c.Redirect(http.StatusFound, result.Next)
	})
}

func setCloooudStateCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: cloooudStateCookie, Value: value, Path: "/api/auth/cloooud", MaxAge: maxAge, HttpOnly: true,
		Secure:   c.Request.TLS != nil || strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")), "https"),
		SameSite: http.SameSiteLaxMode,
	})
}
