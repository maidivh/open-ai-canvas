package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/integrations/cloooud"
	"infinite-canvas/backend/internal/kernel"
)

// [小洞免登定制] 此文件是上游 HTTP 鉴权的补充，由 auth.go 的 currentUser 统一调用。
// 页面账号仅用于发现共享 Cookie 已切换，不能替代会话认证或资源归属检查。
func validateAccountScope(c *gin.Context, userID string) error {
	if c.Request.Method == http.MethodGet && c.Request.URL.Path == "/api/auth/session" {
		return nil // 新页面先读取真实会话，再建立页面自己的账号作用域。
	}
	expected := c.GetHeader("X-Canvas-User-ID")
	guard, _ := c.Cookie(cloooud.AccountScopeCookie)
	// 旧版前端没有账号头：免登后拒绝其写请求，防止尚未刷新的旧标签把数据写入新账号。
	// 媒体 GET 可不带头以兼容 img/video；带头的读请求仍检查账号，避免复用错误用户的数据。
	write := c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && c.Request.Method != http.MethodOptions
	if (expected != "" && expected != userID) || (guard != "" && write && expected == "") {
		return kernel.NewAppError(http.StatusConflict, "账号已切换，请刷新页面后继续操作")
	}
	return nil
}

// 原生登录和退出共用的清理入口；小洞免登回调成功时会重新设置该标记。
func clearAccountScopeCookie(c *gin.Context, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{Name: cloooud.AccountScopeCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}
