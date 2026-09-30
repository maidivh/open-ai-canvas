package cloooud

import (
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"infinite-canvas/backend/internal/kernel"
)

type cloooudConfig struct {
	ClientID, ClientSecret, Issuer, AuthorizationURL, ExchangeURL, RedirectURL string
	SiteID                                                                     int64
	AutoRegister                                                               bool
}

var cloooudClientPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
var cloooudSecretPattern = regexp.MustCompile(`^[\x21-\x7e]{32,512}$`)

func readCloooudConfig() (cloooudConfig, error) {
	get := func(key string) string { return strings.TrimSpace(os.Getenv("CANVAS_CLOOOUD_SSO_" + key)) }
	if get("ENABLED") != "true" {
		return cloooudConfig{}, kernel.Forbidden("小洞单点登录尚未启用")
	}
	siteID, err := strconv.ParseInt(get("SITE_ID"), 10, 64)
	cfg := cloooudConfig{
		ClientID: get("CLIENT_ID"), ClientSecret: get("CLIENT_SECRET"), Issuer: get("ISSUER"),
		AuthorizationURL: get("AUTHORIZATION_URL"), ExchangeURL: get("EXCHANGE_URL"), RedirectURL: get("REDIRECT_URL"),
		SiteID: siteID, AutoRegister: get("AUTO_REGISTER") == "true",
	}
	if err != nil || siteID <= 0 || !cloooudClientPattern.MatchString(cfg.ClientID) || !cloooudSecretPattern.MatchString(cfg.ClientSecret) {
		return cfg, kernel.BadAuthRequest("小洞单点登录配置不完整")
	}
	if value := get("AUTO_REGISTER"); value != "" && value != "true" && value != "false" {
		return cfg, kernel.BadAuthRequest("小洞单点登录自动建号配置必须为 true 或 false")
	}
	for _, raw := range []string{cfg.Issuer, cfg.AuthorizationURL, cfg.ExchangeURL, cfg.RedirectURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.ContainsAny(raw, "\\#\r\n\t") || len(raw) > 2048 ||
			(u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackOAuthHost(u.Hostname()))) {
			return cfg, kernel.BadAuthRequest("小洞单点登录地址必须为无凭据和查询参数的 HTTPS 地址，本地回环可用 HTTP")
		}
	}
	callback, _ := url.Parse(cfg.RedirectURL)
	if callback.EscapedPath() != "/api/auth/cloooud/callback" {
		return cfg, kernel.BadAuthRequest("小洞单点登录回调路径必须为 /api/auth/cloooud/callback")
	}
	return cfg, nil
}

func safeCloooudNext(value string) string {
	value = strings.TrimSpace(value)
	u, err := url.Parse(value)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") ||
		strings.ContainsAny(u.Path, "\\\r\n") || strings.ContainsAny(value, "\r\n") || strings.HasPrefix(u.Path, "/api/") ||
		u.Path == "/api" || strings.HasPrefix(u.Path, "/login") || strings.HasPrefix(u.Path, "/register") {
		return "/create"
	}
	return u.RequestURI()
}

func isLoopbackOAuthHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
