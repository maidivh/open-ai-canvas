package repository

import (
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

func (r *Repository) WantraceGroupsForUser(userID string) ([]model.WantraceAssetGroup, error) {
	var groups []model.WantraceAssetGroup
	err := r.db.Where("user_id = ?", userID).Order("created_at desc").Find(&groups).Error
	return groups, err
}

func (r *Repository) WantraceGroupForUser(userID, id string) (*model.WantraceAssetGroup, error) {
	var group model.WantraceAssetGroup
	err := r.db.Where("user_id = ? AND id = ?", userID, id).First(&group).Error
	if err != nil {
		return nil, err
	}
	return &group, nil
}

func (r *Repository) WantraceGroupByRemoteIDForUser(userID, remoteID string) (*model.WantraceAssetGroup, error) {
	var group model.WantraceAssetGroup
	err := r.db.Where("user_id = ? AND remote_id = ?", userID, remoteID).First(&group).Error
	if err != nil {
		return nil, err
	}
	return &group, nil
}

func (r *Repository) CreateWantraceGroup(group *model.WantraceAssetGroup) error {
	return r.db.Create(group).Error
}

func (r *Repository) SaveWantraceGroup(group *model.WantraceAssetGroup) error {
	return r.db.Save(group).Error
}

func (r *Repository) CountWantraceRealGroups(userID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.WantraceAssetGroup{}).Where("user_id = ? AND kind = ?", userID, "real").Count(&count).Error
	return count, err
}

func (r *Repository) WantraceAssetsForGroups(groupIDs []string) ([]model.WantraceAsset, error) {
	if len(groupIDs) == 0 {
		return []model.WantraceAsset{}, nil
	}
	var assets []model.WantraceAsset
	err := r.db.Where("group_id IN ?", groupIDs).Order("created_at desc").Find(&assets).Error
	return assets, err
}

func (r *Repository) WantraceAssetForUser(userID, id string) (*model.WantraceAsset, error) {
	var asset model.WantraceAsset
	err := r.db.Where("user_id = ? AND id = ?", userID, id).First(&asset).Error
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

func (r *Repository) WantraceActiveAssetForResource(userID, resourceID string) (*model.WantraceAsset, error) {
	var asset model.WantraceAsset
	err := r.db.Where("user_id = ? AND resource_id = ? AND status = ? AND asset_uri <> ''", userID, resourceID, "Active").
		Order("updated_at desc").First(&asset).Error
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

func (r *Repository) WantraceAssetByURI(userID, assetURI string) (*model.WantraceAsset, error) {
	var asset model.WantraceAsset
	err := r.db.Where("user_id = ? AND asset_uri = ? AND status = ?", userID, assetURI, "Active").First(&asset).Error
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

func (r *Repository) CreateWantraceAsset(asset *model.WantraceAsset) error {
	return r.db.Create(asset).Error
}

func (r *Repository) SaveWantraceAsset(asset *model.WantraceAsset) error {
	return r.db.Save(asset).Error
}

func (r *Repository) WantraceVerificationsForUser(userID string) ([]model.WantraceVerification, error) {
	var items []model.WantraceVerification
	err := r.db.Where("user_id = ?", userID).Order("created_at desc").Find(&items).Error
	return items, err
}

func (r *Repository) WantraceVerificationForUser(userID, id string) (*model.WantraceVerification, error) {
	var item model.WantraceVerification
	err := r.db.Where("user_id = ? AND id = ?", userID, id).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *Repository) CountPendingWantraceVerifications(userID string, now time.Time) (int64, error) {
	var count int64
	err := r.db.Model(&model.WantraceVerification{}).
		Where("user_id = ? AND status = ? AND expires_at > ?", userID, "pending", now).
		Count(&count).Error
	return count, err
}

func (r *Repository) CreateWantraceVerification(item *model.WantraceVerification) error {
	return r.db.Create(item).Error
}

func (r *Repository) SaveWantraceVerification(item *model.WantraceVerification) error {
	return r.db.Save(item).Error
}

func (r *Repository) TxWantrace(fn func(tx *gorm.DB) error) error {
	return r.db.Transaction(fn)
}
