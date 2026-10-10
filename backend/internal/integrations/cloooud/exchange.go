package cloooud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/outbound"
)

func exchangeCloooudCode(ctx context.Context, cfg cloooudConfig, code, verifier string) (cloooudProfile, error) {
	var profile cloooudProfile
	if _, err := outbound.ValidateOutboundURL(cfg.ExchangeURL); err != nil {
		return profile, kernel.BadAuthRequest("小洞身份服务地址未获准访问，请检查服务端地址与私网白名单")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret},
		"redirect_uri": {cfg.RedirectURL}, "code": {code}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.ExchangeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return profile, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Site-id", strconv.FormatInt(cfg.SiteID, 10))
	req.Header.Set("channel", "pc")
	client := outbound.OutboundHTTPClient(15 * time.Second)
	// 307/308 会转发包含客户端密钥的请求体，兑换接口禁止跟随任何重定向。
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("SSO exchange redirects are forbidden") }
	resp, err := client.Do(req)
	if err != nil {
		return profile, errors.New("小洞身份服务暂时不可用，请重新进入")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return profile, errors.New("小洞身份验证失败，请重新登录小洞")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return profile, errors.New("小洞身份响应无效")
	}
	var payload struct {
		Code int            `json:"code"`
		Data cloooudProfile `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Code != 1 {
		return profile, errors.New("小洞身份验证失败，请重新登录小洞")
	}
	profile = payload.Data
	if profile.Issuer != cfg.Issuer || profile.SiteID != cfg.SiteID || profile.MemberID <= 0 {
		return cloooudProfile{}, errors.New("小洞返回的身份来源或站点不匹配")
	}
	return profile, nil
}
