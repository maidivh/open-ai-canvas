package app

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// wantraceLibraryAssetID 是海外版素材在用户素材库中的稳定 ID；同一素材重复加入不产生副本。
func wantraceLibraryAssetID(assetID string) string {
	return "wantrace-" + assetID
}

func wantraceCertificationPayload(asset model.WantraceAsset, group model.WantraceAssetGroup) map[string]any {
	payload := map[string]any{
		"provider":        "wantrace",
		"kind":            firstNonEmpty(group.Kind, wantraceGroupKindReal),
		"portraitAssetId": asset.ID,
		"groupId":         group.ID,
		"groupName":       group.Name,
		"remoteGroupId":   group.RemoteID,
		"certifiedAt":     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if group.ExpiresAt != nil {
		payload["expiresAt"] = group.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	return payload
}

// AddWantraceAssetToLibrary 把海外版已通过的真人图片加入素材库「方舟认证」分类。
// asset_uri 只有 Active 状态才有值；素材库记录同时保存本地资源与 URI，生成时按渠道切换引用方式。
func (s *Service) AddWantraceAssetToLibrary(actor *model.User, id string) (string, error) {
	if actor == nil {
		return "", Unauthorized("请先登录")
	}
	asset, err := s.repo.WantraceAssetForUser(actor.ID, strings.TrimSpace(id))
	if err != nil {
		return "", NotFound("素材不存在")
	}
	if !strings.EqualFold(asset.Status, wantraceAssetStatusActive) || strings.TrimSpace(asset.AssetURI) == "" {
		return "", BadAuthRequest("只有已通过审核的图片可以加入素材库")
	}
	group, err := s.repo.WantraceGroupForUser(actor.ID, asset.GroupID)
	if err != nil {
		return "", NotFound("真人素材组不存在")
	}
	if group.ExpiresAt != nil && !group.ExpiresAt.After(time.Now()) {
		return "", BadAuthRequest("该真人素材组授权已过期，请重新认证")
	}
	resource, err := s.repo.ResourceForUser(actor.ID, asset.ResourceID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", NotFound("该素材的原图已被删除，无法加入素材库")
	}
	if err != nil {
		return "", err
	}
	libraryID := wantraceLibraryAssetID(asset.ID)
	certification := wantraceCertificationPayload(*asset, *group)

	updated, err := s.repo.UpdateUserAssetPayload(actor.ID, libraryID, func(record *model.Asset, payload map[string]any) (bool, error) {
		if record.Kind != "image" {
			return false, nil
		}
		data, _ := payload["data"].(map[string]any)
		if storageKey, _ := data["storageKey"].(string); strings.TrimSpace(storageKey) != "resource:"+resource.ID {
			return false, nil
		}
		payload["wantraceAssetUri"] = asset.AssetURI
		payload["wantraceCertification"] = certification
		record.Category = model.AssetCategoryArk
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if updated {
		return s.saveWantraceLibraryLink(asset, libraryID)
	}
	if _, err := s.repo.AssetForUser(actor.ID, libraryID); err == nil {
		return "", BadAuthRequest("素材库中的图片已被替换，无法重新绑定素材")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}

	width, height := s.arkResourceDimensions(resource)
	if width <= 0 || height <= 0 {
		return "", errors.New("无法读取素材图片尺寸，未写入素材库")
	}
	now := time.Now().UTC()
	url := resourceFileURL(resource.ID)
	title := truncateRunes(firstNonEmpty(strings.TrimSpace(asset.Name), group.Name), 120)
	payload, err := json.Marshal(map[string]any{
		"id": libraryID, "kind": "image", "category": model.AssetCategoryArk, "status": model.AssetVersionStatusConfirmed,
		"source": "方舟素材库", "title": title, "coverUrl": url, "tags": []string{"真人"},
		"createdAt": now.Format(time.RFC3339Nano), "updatedAt": now.Format(time.RFC3339Nano),
		"data":                  map[string]any{"dataUrl": url, "storageKey": "resource:" + resource.ID, "mimeType": resource.MimeType, "bytes": resource.Size, "width": width, "height": height},
		"metadata":              map[string]any{"source": "wantrace-asset"},
		"wantraceAssetUri":      asset.AssetURI,
		"wantraceCertification": certification,
	})
	if err != nil {
		return "", err
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return "", err
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	usage, err := s.repo.UserStorageUsage(actor.ID)
	if err != nil {
		return "", err
	}
	if err := validateStructuredStorageQuotaWithPolicy(usage, "asset", true, int64(len(payload)), policy.Resource); err != nil {
		return "", err
	}
	if err := s.repo.UpsertAsset(&model.Asset{
		ID: libraryID, UserID: actor.ID, Kind: "image", Category: model.AssetCategoryArk, Status: model.AssetVersionStatusConfirmed,
		Title: title, PayloadJSON: string(payload), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return "", err
	}
	return s.saveWantraceLibraryLink(asset, libraryID)
}

func (s *Service) saveWantraceLibraryLink(asset *model.WantraceAsset, libraryID string) (string, error) {
	if asset.LibraryAssetID == libraryID {
		return libraryID, nil
	}
	asset.LibraryAssetID = libraryID
	if err := s.repo.SaveWantraceAsset(asset); err != nil {
		return "", err
	}
	return libraryID, nil
}
