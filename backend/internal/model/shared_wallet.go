package model

import "time"

const SharedWalletSettingKey = "shared_wallet"

// SecretCipher is only serialized inside encrypted server-side settings snapshots.
// HTTP handlers return a separate public view, never this persistence type.
type SharedWalletSettings struct {
	Mode         string `json:"mode"`
	BaseURL      string `json:"baseUrl"`
	ClientID     string `json:"clientId"`
	SiteID       int64  `json:"siteId"`
	Issuer       string `json:"issuer"`
	RechargeURL  string `json:"rechargeUrl"`
	SecretCipher string `json:"secretCipher"`
	Version      int64  `json:"version"`
}

type WalletOperation struct {
	ID             string    `gorm:"primaryKey;size:96" json:"id"`
	BillingOrderID string    `gorm:"size:36;uniqueIndex:idx_wallet_order_action,priority:1;index" json:"billingOrderId"`
	Action         string    `gorm:"size:20;uniqueIndex:idx_wallet_order_action,priority:2" json:"action"`
	Point          int64     `json:"point"`
	State          string    `gorm:"size:20;index" json:"state"`
	Attempts       int       `json:"attempts"`
	Error          string    `gorm:"size:200" json:"error,omitempty"`
	ResponseJSON   string    `gorm:"type:text" json:"-"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}
