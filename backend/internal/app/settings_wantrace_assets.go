package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

const wantraceAssetSettingKey = "wantrace_assets"

const (
	defaultWantraceRealGroupLimit = 5
	defaultWantraceAuthorization  = 365
	maxWantraceRealGroupLimit     = 200
	maxWantraceAuthorizationDays  = 3650
)

type WantraceAssetSettingRequest struct {
	Enabled               bool   `json:"enabled"`
	RealEnabled           bool   `json:"realEnabled"`
	APIKey                string `json:"apiKey"`
	ChannelID             int64  `json:"channelId"`
	RealGroupLimit        int    `json:"realGroupLimit"`
	RealAuthorizationDays int    `json:"realAuthorizationDays"`
}

type PublicWantraceAssetSetting struct {
	Enabled               bool      `json:"enabled"`
	RealEnabled           bool      `json:"realEnabled"`
	BaseURL               string    `json:"baseUrl"`
	ChannelID             int64     `json:"channelId"`
	ChannelName           string    `json:"channelName"`
	HasAPIKey             bool      `json:"hasApiKey"`
	RealGroupLimit        int       `json:"realGroupLimit"`
	RealAuthorizationDays int       `json:"realAuthorizationDays"`
	UpdatedBy             string    `json:"updatedBy"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

type wantraceAssetSettingValue struct {
	Enabled               bool   `json:"enabled"`
	RealEnabled           bool   `json:"realEnabled"`
	APIKey                string `json:"apiKey"`
	ChannelID             int64  `json:"channelId"`
	ChannelName           string `json:"channelName"`
	RealGroupLimit        int    `json:"realGroupLimit"`
	RealAuthorizationDays int    `json:"realAuthorizationDays"`
}

func (value wantraceAssetSettingValue) credentialsReady() bool {
	return strings.TrimSpace(value.APIKey) != "" && value.ChannelID > 0
}

func normalizeWantraceAssetSetting(value wantraceAssetSettingValue) wantraceAssetSettingValue {
	value.APIKey = strings.TrimSpace(value.APIKey)
	value.ChannelName = strings.TrimSpace(value.ChannelName)
	if value.RealGroupLimit <= 0 {
		value.RealGroupLimit = defaultWantraceRealGroupLimit
	}
	if value.RealAuthorizationDays <= 0 {
		value.RealAuthorizationDays = defaultWantraceAuthorization
	}
	return value
}

func (s *Service) readWantraceAssetSetting() (*model.SystemSetting, wantraceAssetSettingValue, error) {
	setting, err := s.repo.SystemSetting(wantraceAssetSettingKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, normalizeWantraceAssetSetting(wantraceAssetSettingValue{}), nil
	}
	if err != nil {
		return nil, wantraceAssetSettingValue{}, err
	}
	value := wantraceAssetSettingValue{}
	if strings.TrimSpace(setting.ValueJSON) != "" {
		if err := json.Unmarshal([]byte(setting.ValueJSON), &value); err != nil {
			return nil, wantraceAssetSettingValue{}, errors.New("素材库配置格式无效")
		}
	}
	secret, err := s.decryptSettingSecret(value.APIKey)
	if err != nil {
		return nil, wantraceAssetSettingValue{}, err
	}
	value.APIKey = secret
	return setting, normalizeWantraceAssetSetting(value), nil
}

func (s *Service) saveWantraceAssetSetting(setting *model.SystemSetting, value wantraceAssetSettingValue) error {
	stored := normalizeWantraceAssetSetting(value)
	encrypted, err := s.encryptSettingSecret(stored.APIKey)
	if err != nil {
		return err
	}
	stored.APIKey = encrypted
	encoded, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	setting.Key = wantraceAssetSettingKey
	setting.ValueJSON = string(encoded)
	return s.repo.SaveSystemSetting(setting)
}

func publicWantraceAssetSetting(setting *model.SystemSetting, value wantraceAssetSettingValue) PublicWantraceAssetSetting {
	result := PublicWantraceAssetSetting{
		Enabled:               value.Enabled,
		RealEnabled:           value.RealEnabled,
		BaseURL:               wantraceAssetAPIBase,
		ChannelID:             value.ChannelID,
		ChannelName:           value.ChannelName,
		HasAPIKey:             strings.TrimSpace(value.APIKey) != "",
		RealGroupLimit:        value.RealGroupLimit,
		RealAuthorizationDays: value.RealAuthorizationDays,
	}
	if setting != nil {
		result.UpdatedBy = setting.UpdatedBy
		result.UpdatedAt = setting.UpdatedAt
	}
	return result
}

// disableArkPortraitLibrariesForWantrace 在开放海外版时关闭国内版的用户素材组。
// 只关闭 realEnabled / virtualEnabled，保留可信素材同步等仅管理员使用的能力。
func (s *Service) disableArkPortraitLibrariesForWantrace(actor *model.User) error {
	settingRecord, current, err := s.readArkPrivateAssetSetting()
	if err != nil {
		return err
	}
	if !current.RealEnabled && !current.VirtualEnabled {
		return nil
	}
	current.RealEnabled = false
	current.VirtualEnabled = false
	setting := &model.SystemSetting{Key: arkPrivateAssetSettingKey, UpdatedBy: actor.ID}
	if settingRecord != nil {
		setting.CreatedAt = settingRecord.CreatedAt
	}
	if err := s.saveArkPrivateAssetSetting(setting, current); err != nil {
		return err
	}
	return s.appendAdminAudit(actor, "ark_private_assets.auto_disable", "system_setting", arkPrivateAssetSettingKey, "开放海外版素材库，自动关闭国内版用户素材组", map[string]any{"reason": "wantrace_enabled"})
}

// disableWantraceLibraryForArkPortrait 在开放国内版用户素材组时关闭海外版入口。
func (s *Service) disableWantraceLibraryForArkPortrait(actor *model.User) error {
	settingRecord, current, err := s.readWantraceAssetSetting()
	if err != nil {
		return err
	}
	if !current.Enabled && !current.RealEnabled {
		return nil
	}
	current.Enabled = false
	current.RealEnabled = false
	setting := &model.SystemSetting{Key: wantraceAssetSettingKey, UpdatedBy: actor.ID}
	if settingRecord != nil {
		setting.CreatedAt = settingRecord.CreatedAt
	}
	if err := s.saveWantraceAssetSetting(setting, current); err != nil {
		return err
	}
	return s.appendAdminAudit(actor, "wantrace_assets.auto_disable", "system_setting", wantraceAssetSettingKey, "开放国内版素材库，自动关闭海外版入口", map[string]any{"reason": "ark_portrait_enabled"})
}

func (s *Service) AdminWantraceAssetSetting(actor *model.User) (*PublicWantraceAssetSetting, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	setting, value, err := s.readWantraceAssetSetting()
	if err != nil {
		return nil, err
	}
	public := publicWantraceAssetSetting(setting, value)
	return &public, nil
}

type WantraceChannelView struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

func (s *Service) AdminWantraceChannels(ctx context.Context, actor *model.User, apiKey string) ([]WantraceChannelView, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		_, value, err := s.readWantraceAssetSetting()
		if err != nil {
			return nil, err
		}
		apiKey = value.APIKey
	}
	if apiKey == "" {
		return nil, BadAuthRequest("请先填写海外版 API Key")
	}
	channels, err := wantraceListChannels(ctx, apiKey)
	if err != nil {
		return nil, WrapAppError(502, "读取海外版渠道失败："+err.Error(), err)
	}
	result := make([]WantraceChannelView, 0, len(channels))
	for _, channel := range channels {
		result = append(result, WantraceChannelView{ID: channel.ID, Name: channel.Name, Group: channel.Group})
	}
	return result, nil
}

type WantraceAssetTestResult struct {
	OK          bool                  `json:"ok"`
	ChannelID   int64                 `json:"channelId"`
	ChannelName string                `json:"channelName"`
	Channels    []WantraceChannelView `json:"channels"`
}

// TestAdminWantraceAssetSetting 用已保存的 Key 读取渠道列表，验证 Key 与渠道权限。
func (s *Service) TestAdminWantraceAssetSetting(ctx context.Context, actor *model.User) (*WantraceAssetTestResult, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	_, value, err := s.readWantraceAssetSetting()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(value.APIKey) == "" {
		return nil, BadAuthRequest("请先保存海外版 API Key")
	}
	channels, err := s.AdminWantraceChannels(ctx, actor, value.APIKey)
	if err != nil {
		return nil, err
	}
	result := &WantraceAssetTestResult{OK: true, ChannelID: value.ChannelID, ChannelName: value.ChannelName, Channels: channels}
	for _, channel := range channels {
		if channel.ID == value.ChannelID {
			result.ChannelName = channel.Name
			break
		}
	}
	return result, nil
}

func (s *Service) UpdateWantraceAssetSetting(ctx context.Context, actor *model.User, req WantraceAssetSettingRequest) (*PublicWantraceAssetSetting, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	settingRecord, current, err := s.readWantraceAssetSetting()
	if err != nil {
		return nil, err
	}
	next := normalizeWantraceAssetSetting(wantraceAssetSettingValue{
		Enabled:               req.Enabled,
		RealEnabled:           req.RealEnabled,
		APIKey:                req.APIKey,
		ChannelID:             req.ChannelID,
		RealGroupLimit:        req.RealGroupLimit,
		RealAuthorizationDays: req.RealAuthorizationDays,
	})
	if next.APIKey == "" {
		next.APIKey = current.APIKey
	}
	// 请求不提交渠道名；沿用同一渠道的展示名，避免关闭开关后配置页丢失渠道名称。
	if next.ChannelID == 0 {
		next.ChannelName = ""
	} else if next.ChannelID == current.ChannelID {
		next.ChannelName = current.ChannelName
	}
	if req.RealGroupLimit < 0 || req.RealGroupLimit > maxWantraceRealGroupLimit {
		return nil, BadAuthRequest(fmt.Sprintf("真人素材组上限需在 1-%d 之间", maxWantraceRealGroupLimit))
	}
	if req.RealAuthorizationDays < 0 || req.RealAuthorizationDays > maxWantraceAuthorizationDays {
		return nil, BadAuthRequest(fmt.Sprintf("真人授权有效期需在 1-%d 天之间", maxWantraceAuthorizationDays))
	}
	if req.RealGroupLimit == 0 {
		next.RealGroupLimit = current.RealGroupLimit
	}
	if req.RealAuthorizationDays == 0 {
		next.RealAuthorizationDays = current.RealAuthorizationDays
	}
	if next.Enabled || next.RealEnabled {
		if next.APIKey == "" {
			return nil, BadAuthRequest("请填写海外版 API Key")
		}
		channels, err := wantraceListChannels(ctx, next.APIKey)
		if err != nil {
			return nil, WrapAppError(502, "读取海外版渠道失败："+err.Error(), err)
		}
		if next.ChannelID == 0 && len(channels) > 0 {
			next.ChannelID = channels[0].ID
		}
		found := false
		for _, channel := range channels {
			if channel.ID == next.ChannelID {
				next.ChannelName = channel.Name
				found = true
				break
			}
		}
		if !found {
			return nil, BadAuthRequest("海外版渠道不存在或当前 Key 无权访问")
		}
	}
	setting := &model.SystemSetting{Key: wantraceAssetSettingKey, UpdatedBy: actor.ID}
	if settingRecord != nil {
		setting.CreatedAt = settingRecord.CreatedAt
	}
	if err := s.saveWantraceAssetSetting(setting, next); err != nil {
		return nil, err
	}
	// 两套素材库的用户入口互斥；开启海外版真人素材组时关闭国内版入口。
	if next.Enabled && next.RealEnabled {
		if err := s.disableArkPortraitLibrariesForWantrace(actor); err != nil {
			return nil, err
		}
	}
	if err := s.appendAdminAudit(actor, "wantrace_assets.update", "system_setting", wantraceAssetSettingKey, "更新海外版素材库配置", map[string]any{
		"enabled": next.Enabled, "realEnabled": next.RealEnabled,
		"channelId": next.ChannelID, "channelName": next.ChannelName,
	}); err != nil {
		return nil, err
	}
	public := publicWantraceAssetSetting(setting, next)
	return &public, nil
}
