package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

const (
	wantraceGroupKindReal        = "real"
	wantraceAssetStatusActive    = "Active"
	wantraceAssetStatusFailed    = "Failed"
	wantraceAssetStatusUncertain = "Uncertain"
	wantraceVerificationPending  = "pending"
	wantraceVerificationVerified = "verified"
	wantraceVerificationExpired  = "expired"
	wantraceVerificationFailed   = "failed"
	wantraceMaxImageBytes        = 30 * 1024 * 1024
)

type WantraceAssetView struct {
	ID             string    `json:"id"`
	GroupID        string    `json:"groupId"`
	ResourceID     string    `json:"resourceId"`
	Name           string    `json:"name"`
	AssetType      string    `json:"assetType"`
	Status         string    `json:"status"`
	AssetURI       string    `json:"assetUri"`
	Error          string    `json:"error,omitempty"`
	LibraryAssetID string    `json:"libraryAssetId,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type WantraceGroupView struct {
	ID          string              `json:"id"`
	Kind        string              `json:"kind"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	ChannelID   int64               `json:"channelId"`
	RemoteID    string              `json:"remoteId"`
	UpstreamID  string              `json:"upstreamId"`
	State       string              `json:"state"`
	ExpiresAt   *time.Time          `json:"expiresAt,omitempty"`
	Expired     bool                `json:"expired"`
	CreatedAt   time.Time           `json:"createdAt"`
	Assets      []WantraceAssetView `json:"assets"`
}

type WantraceVerificationView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	GroupID   string    `json:"groupId,omitempty"`
	H5Link    string    `json:"h5Link,omitempty"`
	Error     string    `json:"error,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

type WantraceAssetConfigView struct {
	ChannelID             int64 `json:"channelId"`
	RealGroupLimit        int   `json:"realGroupLimit"`
	RealAuthorizationDays int   `json:"realAuthorizationDays"`
}

type WantraceOverview struct {
	Enabled       bool                       `json:"enabled"`
	RealEnabled   bool                       `json:"realEnabled"`
	Config        WantraceAssetConfigView    `json:"config"`
	Groups        []WantraceGroupView        `json:"groups"`
	Verifications []WantraceVerificationView `json:"verifications"`
	Usage         int64                      `json:"usage"`
}

func (s *Service) wantraceCredentialSetting() (wantraceAssetSettingValue, error) {
	_, value, err := s.readWantraceAssetSetting()
	if err != nil {
		return value, err
	}
	if !value.Enabled || !value.RealEnabled {
		return value, BadAuthRequest("真人素材库尚未开放")
	}
	if !value.credentialsReady() {
		return value, BadAuthRequest("素材库尚未配置完整，请联系管理员")
	}
	return value, nil
}

func (s *Service) WantraceAssetOverview(actor *model.User) (*WantraceOverview, error) {
	if actor == nil {
		return nil, Unauthorized("请先登录")
	}
	_, value, err := s.readWantraceAssetSetting()
	if err != nil {
		return nil, err
	}
	overview := &WantraceOverview{
		Enabled:     value.Enabled,
		RealEnabled: value.RealEnabled,
		Config: WantraceAssetConfigView{
			ChannelID:             value.ChannelID,
			RealGroupLimit:        value.RealGroupLimit,
			RealAuthorizationDays: value.RealAuthorizationDays,
		},
		Groups:        []WantraceGroupView{},
		Verifications: []WantraceVerificationView{},
	}
	if !value.Enabled || !value.RealEnabled {
		return overview, nil
	}
	groups, err := s.repo.WantraceGroupsForUser(actor.ID)
	if err != nil {
		return nil, err
	}
	groupIDs := make([]string, 0, len(groups))
	for _, group := range groups {
		groupIDs = append(groupIDs, group.ID)
	}
	assets, err := s.repo.WantraceAssetsForGroups(groupIDs)
	if err != nil {
		return nil, err
	}
	assetByGroup := map[string][]WantraceAssetView{}
	for _, asset := range assets {
		assetByGroup[asset.GroupID] = append(assetByGroup[asset.GroupID], wantraceAssetView(asset))
	}
	now := time.Now()
	for _, group := range groups {
		view := WantraceGroupView{
			ID: group.ID, Kind: group.Kind, Name: group.Name, Description: group.Description,
			ChannelID: group.ChannelID, RemoteID: group.RemoteID, UpstreamID: group.UpstreamID,
			State: group.State, ExpiresAt: group.ExpiresAt, CreatedAt: group.CreatedAt,
			Assets: assetByGroup[group.ID],
		}
		if view.Assets == nil {
			view.Assets = []WantraceAssetView{}
		}
		if group.ExpiresAt != nil && !group.ExpiresAt.After(now) {
			view.Expired = true
		}
		overview.Groups = append(overview.Groups, view)
	}
	overview.Usage = int64(len(groups))
	verifications, err := s.repo.WantraceVerificationsForUser(actor.ID)
	if err != nil {
		return nil, err
	}
	for _, item := range verifications {
		if item.Status == wantraceVerificationPending && !item.ExpiresAt.After(now) {
			item.Status = wantraceVerificationExpired
			_ = s.repo.SaveWantraceVerification(&item)
		}
		view := wantraceVerificationView(item)
		// 未完成的认证需要允许本人刷新页面后继续扫码；链接仍只对归属用户返回。
		if item.Status == wantraceVerificationPending && item.H5URLCipher != "" {
			if link, err := s.decryptSettingSecret(item.H5URLCipher); err == nil {
				view.H5Link = link
			}
		}
		overview.Verifications = append(overview.Verifications, view)
	}
	return overview, nil
}

func (s *Service) CreateWantraceVerification(ctx context.Context, actor *model.User, name string) (*WantraceVerificationView, error) {
	if actor == nil {
		return nil, Unauthorized("请先登录")
	}
	setting, err := s.wantraceCredentialSetting()
	if err != nil {
		return nil, err
	}
	groupCount, err := s.repo.CountWantraceRealGroups(actor.ID)
	if err != nil {
		return nil, err
	}
	pendingCount, err := s.repo.CountPendingWantraceVerifications(actor.ID, time.Now())
	if err != nil {
		return nil, err
	}
	if groupCount+pendingCount >= int64(setting.RealGroupLimit) {
		return nil, BadAuthRequest(fmt.Sprintf("真人素材组已达上限（%d）", setting.RealGroupLimit))
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "真人素材"
	}
	name = truncateRunes(name, 80)
	remote, err := wantraceCreateVerification(ctx, setting.APIKey, setting.ChannelID)
	if err != nil {
		return nil, WrapAppError(502, "发起真人认证失败："+err.Error(), err)
	}
	if remote.ID == "" || !validWantraceH5URL(remote.H5URL) {
		return nil, NewAppError(502, "素材库未返回有效的认证链接")
	}
	expiresAt := time.Unix(remote.ExpiresAt, 0)
	if remote.ExpiresAt <= 0 || expiresAt.Before(time.Now()) {
		expiresAt = time.Now().Add(30 * time.Minute)
	}
	cipher, err := s.encryptSettingSecret(remote.H5URL)
	if err != nil {
		return nil, err
	}
	item := &model.WantraceVerification{
		ID: newID(), UserID: actor.ID, ChannelID: setting.ChannelID,
		RemoteID: remote.ID, Name: name, H5URLCipher: cipher,
		Status: wantraceVerificationPending, ExpiresAt: expiresAt,
	}
	if err := s.repo.CreateWantraceVerification(item); err != nil {
		return nil, err
	}
	view := wantraceVerificationView(*item)
	// H5 链接只返回给发起人一次；服务端仅保存密文，不在列表接口重复下发。
	view.H5Link = remote.H5URL
	return &view, nil
}

func (s *Service) CheckWantraceVerification(ctx context.Context, actor *model.User, id string) (*WantraceVerificationView, error) {
	if actor == nil {
		return nil, Unauthorized("请先登录")
	}
	setting, err := s.wantraceCredentialSetting()
	if err != nil {
		return nil, err
	}
	item, err := s.repo.WantraceVerificationForUser(actor.ID, strings.TrimSpace(id))
	if err != nil {
		return nil, NotFound("认证会话不存在")
	}
	if item.Status != wantraceVerificationPending {
		view := wantraceVerificationView(*item)
		return &view, nil
	}
	if !item.ExpiresAt.After(time.Now()) {
		item.Status = wantraceVerificationExpired
		_ = s.repo.SaveWantraceVerification(item)
		view := wantraceVerificationView(*item)
		return &view, nil
	}
	remote, err := wantraceCheckVerification(ctx, setting.APIKey, item.RemoteID)
	if err != nil {
		return nil, WrapAppError(502, "查询真人认证结果失败："+err.Error(), err)
	}
	switch strings.ToLower(strings.TrimSpace(remote.Status)) {
	case wantraceVerificationPending:
		view := wantraceVerificationView(*item)
		return &view, nil
	case wantraceVerificationExpired:
		item.Status = wantraceVerificationExpired
		_ = s.repo.SaveWantraceVerification(item)
		view := wantraceVerificationView(*item)
		return &view, nil
	case wantraceVerificationFailed:
		item.Status = wantraceVerificationFailed
		item.Error = firstNonEmpty(strings.TrimSpace(remote.Error), "真人认证未通过")
		_ = s.repo.SaveWantraceVerification(item)
		view := wantraceVerificationView(*item)
		return &view, nil
	case wantraceVerificationVerified:
		group := remote.Group
		if group == nil || strings.TrimSpace(group.ID) == "" || strings.TrimSpace(group.Kind) != wantraceGroupKindReal {
			return nil, NewAppError(502, "素材库未返回有效的真人素材组")
		}
		local, err := s.repo.WantraceGroupByRemoteIDForUser(actor.ID, group.ID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			expiresAt := time.Now().Add(time.Duration(setting.RealAuthorizationDays) * 24 * time.Hour)
			local = &model.WantraceAssetGroup{
				ID: newID(), UserID: actor.ID, Kind: wantraceGroupKindReal,
				Name: firstNonEmpty(item.Name, "真人素材"), Description: "",
				ChannelID: setting.ChannelID, RemoteID: group.ID, UpstreamID: group.UpstreamID,
				State: group.State, ExpiresAt: &expiresAt,
			}
			if err := s.repo.CreateWantraceGroup(local); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
		item.Status = wantraceVerificationVerified
		item.GroupID = local.ID
		item.Error = ""
		if err := s.repo.SaveWantraceVerification(item); err != nil {
			return nil, err
		}
		view := wantraceVerificationView(*item)
		return &view, nil
	default:
		return nil, NewAppError(502, "真人认证返回未知状态："+remote.Status)
	}
}

func (s *Service) CreateWantraceAsset(ctx context.Context, actor *model.User, groupID, resourceID, name string) (*WantraceAssetView, error) {
	if actor == nil {
		return nil, Unauthorized("请先登录")
	}
	setting, err := s.wantraceCredentialSetting()
	if err != nil {
		return nil, err
	}
	group, err := s.repo.WantraceGroupForUser(actor.ID, strings.TrimSpace(groupID))
	if err != nil {
		return nil, NotFound("真人素材组不存在")
	}
	if group.Kind != wantraceGroupKindReal || group.State != "ready" {
		return nil, BadAuthRequest("真人素材组尚未就绪")
	}
	if group.ExpiresAt != nil && !group.ExpiresAt.After(time.Now()) {
		return nil, BadAuthRequest("真人素材组授权已过期，请重新认证")
	}
	resource, err := s.Resource(actor.ID, strings.TrimSpace(resourceID))
	if err != nil {
		return nil, NotFound("图片素材不存在或无权访问")
	}
	if resource.Status != model.ResourceStatusReady {
		return nil, BadAuthRequest("图片素材尚未上传完成")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(resource.MimeType)), "image/") {
		return nil, BadAuthRequest("真人素材组只支持图片")
	}
	if resource.Size > wantraceMaxImageBytes {
		return nil, BadAuthRequest("真人素材图片不能超过 30MB")
	}
	sourceURL, err := s.providerResourceURL(resource, time.Now().Add(time.Hour))
	if err != nil {
		return nil, fmt.Errorf("生成图片公网地址失败：%w", err)
	}
	name = truncateRunes(firstNonEmpty(strings.TrimSpace(name), "真人素材"), 64)
	remote, err := wantraceCreateAsset(ctx, setting.APIKey, group.RemoteID, name, "Image", sourceURL)
	if err != nil {
		return nil, WrapAppError(502, "提交真人素材失败："+err.Error(), err)
	}
	if strings.TrimSpace(remote.ID) == "" {
		return nil, NewAppError(502, "素材库未返回素材 ID")
	}
	asset := &model.WantraceAsset{
		ID: newID(), UserID: actor.ID, GroupID: group.ID, ResourceID: resource.ID,
		RemoteID: remote.ID, AssetURI: remote.AssetURI, AssetType: "Image", Name: name,
		Status: firstNonEmpty(remote.Status, "Processing"), Error: remote.Error,
	}
	if err := s.repo.CreateWantraceAsset(asset); err != nil {
		return nil, err
	}
	view := wantraceAssetView(*asset)
	return &view, nil
}

func (s *Service) RefreshWantraceAsset(ctx context.Context, actor *model.User, id string) (*WantraceAssetView, error) {
	if actor == nil {
		return nil, Unauthorized("请先登录")
	}
	setting, err := s.wantraceCredentialSetting()
	if err != nil {
		return nil, err
	}
	asset, err := s.repo.WantraceAssetForUser(actor.ID, strings.TrimSpace(id))
	if err != nil {
		return nil, NotFound("素材不存在")
	}
	if strings.EqualFold(asset.Status, wantraceAssetStatusActive) || strings.EqualFold(asset.Status, wantraceAssetStatusFailed) {
		view := wantraceAssetView(*asset)
		return &view, nil
	}
	remote, err := wantraceGetAsset(ctx, setting.APIKey, asset.RemoteID)
	if err != nil {
		return nil, WrapAppError(502, "查询素材状态失败："+err.Error(), err)
	}
	asset.Status = firstNonEmpty(remote.Status, asset.Status)
	asset.AssetURI = firstNonEmpty(remote.AssetURI, asset.AssetURI)
	asset.Error = firstNonEmpty(remote.Error, asset.Error)
	if strings.EqualFold(asset.Status, wantraceAssetStatusUncertain) && asset.Error == "" {
		asset.Error = "上游状态不确定，请保留素材 ID 并联系技术支持"
	}
	if err := s.repo.SaveWantraceAsset(asset); err != nil {
		return nil, err
	}
	view := wantraceAssetView(*asset)
	return &view, nil
}

// prepareWantraceAssetReferences 在海外版视频请求前把已认证的本地资源改写成 asset_uri。
// 创作页等入口可能只带 resource:<id>，不能依赖前端是否携带 wantraceAssetUri；
// 未认证的素材保持原 URL，由上游按普通图片规则判断。
func (s *Service) prepareWantraceAssetReferences(userID string, input *canvasGenerationInput) error {
	if input == nil {
		return nil
	}
	for _, references := range [][]providerMedia{input.ReferenceImages, input.ReferenceVideos, input.ReferenceAudios} {
		for index := range references {
			reference := &references[index]
			assetURI := strings.TrimSpace(reference.URL)
			if !strings.HasPrefix(assetURI, "asset://") {
				resourceID := arkPrivateAssetResourceID(*reference)
				if resourceID == "" {
					continue
				}
				asset, err := s.repo.WantraceActiveAssetForResource(userID, resourceID)
				if err != nil {
					continue
				}
				assetURI = strings.TrimSpace(asset.AssetURI)
				if !strings.HasPrefix(assetURI, "asset://") {
					continue
				}
				reference.URL = assetURI
				reference.DataURL = ""
			}
			if _, err := s.repo.WantraceAssetByURI(userID, assetURI); err != nil {
				return Forbidden("素材不存在或尚未通过审核")
			}
		}
	}
	return nil
}

func wantraceAssetView(asset model.WantraceAsset) WantraceAssetView {
	return WantraceAssetView{
		ID: asset.ID, GroupID: asset.GroupID, ResourceID: asset.ResourceID, Name: asset.Name, AssetType: asset.AssetType,
		Status: asset.Status, AssetURI: asset.AssetURI, Error: asset.Error,
		LibraryAssetID: asset.LibraryAssetID, CreatedAt: asset.CreatedAt, UpdatedAt: asset.UpdatedAt,
	}
}

func wantraceVerificationView(item model.WantraceVerification) WantraceVerificationView {
	return WantraceVerificationView{
		ID: item.ID, Name: item.Name, Status: item.Status, GroupID: item.GroupID, Error: item.Error,
		ExpiresAt: item.ExpiresAt, CreatedAt: item.CreatedAt,
	}
}

func validWantraceH5URL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}
