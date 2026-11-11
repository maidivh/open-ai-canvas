package database

import (
	"testing"
	"time"

	"yingce/backend/internal/model"
)

// arkPrivateAssetBindingV93 是 v93 时的绑定表形状：唯一键只有资源与项目。
type arkPrivateAssetBindingV93 struct {
	ID           string `gorm:"primaryKey;size:36"`
	UserID       string `gorm:"size:36"`
	ResourceID   string `gorm:"size:36;uniqueIndex:idx_ark_private_asset_binding_resource_project,priority:1"`
	ProjectName  string `gorm:"size:160;uniqueIndex:idx_ark_private_asset_binding_resource_project,priority:2"`
	AssetGroupID string `gorm:"size:120"`
	ArkAssetID   string `gorm:"size:120"`
	Status       string `gorm:"size:24"`
	Error        string `gorm:"type:text"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (arkPrivateAssetBindingV93) TableName() string { return "ark_private_asset_bindings" }

func TestMigrateArkAssetProviderAddsSiteAndRebuildsIndex(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:ark-asset-provider?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&arkPrivateAssetBindingV93{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&arkPrivateAssetBindingV93{ID: "legacy", ResourceID: "res-1", ProjectName: "default"}).Error; err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := migrateArkAssetProvider(db); err != nil {
			t.Fatalf("migrateArkAssetProvider() attempt %d error = %v", attempt, err)
		}
	}

	var legacy model.ArkPrivateAssetBinding
	if err := db.First(&legacy, "id = ?", "legacy").Error; err != nil {
		t.Fatal(err)
	}
	if legacy.Site != "volcengine" {
		t.Fatalf("legacy site = %q, want volcengine", legacy.Site)
	}
	if !db.Migrator().HasIndex(&model.ArkPrivateAssetBinding{}, "idx_ark_private_asset_binding_resource_project") {
		t.Fatal("binding identity index missing")
	}

	if err := db.Create(&model.ArkPrivateAssetBinding{ID: "videoclaw", ResourceID: "res-1", ProjectName: "default", Site: "videoclaw"}).Error; err != nil {
		t.Fatalf("same resource and project on another site must be allowed: %v", err)
	}
	if err := db.Create(&model.ArkPrivateAssetBinding{ID: "duplicate", ResourceID: "res-1", ProjectName: "default", Site: "videoclaw"}).Error; err == nil {
		t.Fatal("duplicate resource, project and site must be rejected")
	}
}
