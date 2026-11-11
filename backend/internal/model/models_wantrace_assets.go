package model

import "time"

// WantraceAssetGroup 是「橙幻-火山方舟-海外版」网关上的真人素材组。
// RemoteID 是网关返回的本地组 UUID，UpstreamID 是上游方舟素材组 ID；真人组只能通过认证创建。
type WantraceAssetGroup struct {
	ID          string     `json:"id" gorm:"primaryKey;size:36"`
	UserID      string     `json:"userId" gorm:"index;size:36;uniqueIndex:idx_wantrace_group_user_remote,priority:1"`
	Kind        string     `json:"kind" gorm:"size:16;not null;default:real"`
	Name        string     `json:"name" gorm:"size:80"`
	Description string     `json:"description" gorm:"size:300;not null;default:''"`
	ChannelID   int64      `json:"channelId" gorm:"not null;default:0"`
	RemoteID    string     `json:"remoteId" gorm:"index;size:64;uniqueIndex:idx_wantrace_group_user_remote,priority:2"`
	UpstreamID  string     `json:"upstreamId" gorm:"size:120"`
	State       string     `json:"state" gorm:"index;size:24"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// WantraceAsset 是海外版素材组内的一份素材。AssetURI 是网关返回的完整 asset:// 引用，
// 只有 Active 状态才有值；真人组只允许 Image。
type WantraceAsset struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	UserID         string    `json:"userId" gorm:"index;size:36"`
	GroupID        string    `json:"groupId" gorm:"index;size:36"`
	ResourceID     string    `json:"resourceId" gorm:"index;size:36"`
	RemoteID       string    `json:"remoteId" gorm:"index;size:64"`
	AssetURI       string    `json:"assetUri" gorm:"index;size:160"`
	AssetType      string    `json:"assetType" gorm:"size:16"`
	Name           string    `json:"name" gorm:"size:120"`
	Status         string    `json:"status" gorm:"index;size:24"`
	Error          string    `json:"error" gorm:"size:1000;not null;default:''"`
	LibraryAssetID string    `json:"libraryAssetId" gorm:"size:80;not null;default:'';index"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// WantraceVerification 是一次海外版真人认证会话。H5 链接只应返回给发起人，
// 服务端加密保存，30 分钟过期；verified 后写回 GroupID。
type WantraceVerification struct {
	ID          string    `json:"id" gorm:"primaryKey;size:36"`
	UserID      string    `json:"userId" gorm:"index;size:36"`
	ChannelID   int64     `json:"channelId" gorm:"not null;default:0"`
	RemoteID    string    `json:"remoteId" gorm:"index;size:64"`
	Name        string    `json:"name" gorm:"size:80"`
	H5URLCipher string    `json:"-" gorm:"type:text"`
	Status      string    `json:"status" gorm:"index;size:24"`
	GroupID     string    `json:"groupId" gorm:"size:36;not null;default:''"`
	Error       string    `json:"error" gorm:"size:1000;not null;default:''"`
	ExpiresAt   time.Time `json:"expiresAt" gorm:"index"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
