package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// wantraceAssetAPIBase 是「橙幻-火山方舟-海外版」网关地址；素材接口统一走 Bearer。
// 测试可以覆盖该变量指向本地 httptest 服务，生产环境始终固定官方域名。
var wantraceAssetAPIBase = "https://newapi.wantrace.com"

type wantraceAPIError struct {
	Status  int
	Message string
}

func (e *wantraceAPIError) Error() string {
	if e.Message != "" {
		return "素材库请求失败：" + e.Message
	}
	return "素材库请求失败（HTTP " + strconv.Itoa(e.Status) + "）"
}

type wantraceChannel struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

type wantraceGroup struct {
	ID          string `json:"id"`
	ChannelID   int64  `json:"channel_id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
	UpstreamID  string `json:"upstream_id"`
	State       string `json:"state"`
	CreatedAt   int64  `json:"created_at"`
}

type wantraceAsset struct {
	ID        string `json:"id"`
	GroupID   string `json:"group_id"`
	ChannelID int64  `json:"channel_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	AssetType string `json:"asset_type"`
	Status    string `json:"status"`
	AssetURI  string `json:"asset_uri"`
	Error     string `json:"error"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type wantraceVerification struct {
	ID        string         `json:"id"`
	H5URL     string         `json:"h5_url"`
	Status    string         `json:"status"`
	Error     string         `json:"error"`
	ExpiresAt int64          `json:"expires_at"`
	Group     *wantraceGroup `json:"group"`
}

type wantraceEnvelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Error   struct {
		Message string `json:"message"`
	} `json:"error"`
}

func wantraceAssetRequest(ctx context.Context, apiKey, method, path string, body any, target any) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return &wantraceAPIError{Message: "未配置素材库 API Key"}
	}
	var bodyRaw []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyRaw = raw
	}
	client := &http.Client{Timeout: 40 * time.Second}
	// New API 的令牌缓存偶尔把刚校验过的 Key 判为无效；401 是未执行请求，重试一次不会重复创建资源。
	for attempt := 0; attempt < 2; attempt++ {
		var reader io.Reader
		if bodyRaw != nil {
			reader = bytes.NewReader(bodyRaw)
		}
		req, err := http.NewRequestWithContext(ctx, method, wantraceAssetAPIBase+path, reader)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		if bodyRaw != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(req)
		if err != nil {
			return &wantraceAPIError{Message: "连接素材库失败"}
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		if readErr != nil {
			return readErr
		}
		var envelope wantraceEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return &wantraceAPIError{Status: response.StatusCode, Message: "素材库返回格式无效"}
		}
		if response.StatusCode == http.StatusUnauthorized && attempt == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success {
			message := strings.TrimSpace(envelope.Message)
			if message == "" {
				message = strings.TrimSpace(envelope.Error.Message)
			}
			if message == "" {
				message = "HTTP " + strconv.Itoa(response.StatusCode)
			}
			return &wantraceAPIError{Status: response.StatusCode, Message: message}
		}
		if target != nil && len(envelope.Data) > 0 {
			if err := json.Unmarshal(envelope.Data, target); err != nil {
				return fmt.Errorf("解析素材库响应失败：%w", err)
			}
		}
		return nil
	}
	return &wantraceAPIError{Message: "素材库鉴权失败"}
}

func wantraceListChannels(ctx context.Context, apiKey string) ([]wantraceChannel, error) {
	var data struct {
		Channels []wantraceChannel `json:"channels"`
	}
	if err := wantraceAssetRequest(ctx, apiKey, http.MethodGet, "/v1/seedance/assets/channels", nil, &data); err != nil {
		return nil, err
	}
	return data.Channels, nil
}

func wantraceCreateVerification(ctx context.Context, apiKey string, channelID int64) (*wantraceVerification, error) {
	var data wantraceVerification
	if err := wantraceAssetRequest(ctx, apiKey, http.MethodPost, "/v1/seedance/assets/verifications", map[string]any{"channel_id": channelID}, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func wantraceCheckVerification(ctx context.Context, apiKey, remoteID string) (*wantraceVerification, error) {
	var data wantraceVerification
	path := "/v1/seedance/assets/verifications/" + url.PathEscape(strings.TrimSpace(remoteID)) + "/check"
	if err := wantraceAssetRequest(ctx, apiKey, http.MethodPost, path, nil, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func wantraceGetGroup(ctx context.Context, apiKey, remoteID string) (*wantraceGroup, error) {
	var data wantraceGroup
	path := "/v1/seedance/assets/groups/" + url.PathEscape(strings.TrimSpace(remoteID))
	if err := wantraceAssetRequest(ctx, apiKey, http.MethodGet, path, nil, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func wantraceCreateAsset(ctx context.Context, apiKey, remoteGroupID, name, assetType, sourceURL string) (*wantraceAsset, error) {
	payload := map[string]any{"name": name, "asset_type": assetType, "url": sourceURL}
	var data wantraceAsset
	path := "/v1/seedance/assets/groups/" + url.PathEscape(strings.TrimSpace(remoteGroupID)) + "/assets"
	if err := wantraceAssetRequest(ctx, apiKey, http.MethodPost, path, payload, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func wantraceGetAsset(ctx context.Context, apiKey, remoteID string) (*wantraceAsset, error) {
	var data wantraceAsset
	path := "/v1/seedance/assets/assets/" + url.PathEscape(strings.TrimSpace(remoteID))
	if err := wantraceAssetRequest(ctx, apiKey, http.MethodGet, path, nil, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func isWantraceAPIError(err error) bool {
	var target *wantraceAPIError
	return errors.As(err, &target)
}
