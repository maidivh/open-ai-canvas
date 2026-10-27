package database

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func TestMergedSchemaPreservesPublishedHistories(t *testing.T) {
	for _, history := range []struct {
		name       string
		version    int64
		walletAt58 bool
		walletAt61 bool
		want61     string
		want73     string
	}{
		{"fresh", 0, false, false, "drama_production", "shared_wallet_billing"},
		{"main72", 72, false, false, "drama_production", "shared_wallet_billing"},
		{"main60", 60, false, false, "drama_production", "shared_wallet_billing"},
		{"branch61_memory58", 61, false, true, "shared_wallet_billing", "drama_production"},
		{"branch61_wallet58", 61, true, false, "agent_memory_proposals", "drama_production"},
		{"legacy_wallet58", 58, true, false, "agent_memory_proposals", "drama_production"},
	} {
		t.Run(history.name, func(t *testing.T) {
			db, err := Open(Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "merge.db")})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })
			var before []schemaMigration
			if history.version > 0 {
				if err := MigrateSchema(db); err != nil {
					t.Fatal(err)
				}
				// Recreate the recorded history and remove schemas that did not exist there.
				if err := db.Where("version > ?", history.version).Delete(&schemaMigration{}).Error; err != nil {
					t.Fatal(err)
				}
				for _, version := range []int64{58, 61} {
					var name, checksum string
					if (version == 58 && history.walletAt58) || (version == 61 && history.walletAt61) {
						name, checksum = "shared_wallet_billing", "sha256:shared-wallet-billing-v58-20261019"
					} else if version == 61 && history.walletAt58 {
						name, checksum = "agent_memory_proposals", "sha256:agent-memory-proposals-v58"
					}
					if name != "" && version <= history.version {
						if err := db.Model(&schemaMigration{}).Where("version = ?", version).Updates(map[string]any{
							"name": name, "checksum": checksum, "applied_at": time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC),
						}).Error; err != nil {
							t.Fatal(err)
						}
					}
				}
				var absent []any
				if history.version < 72 {
					absent = append(absent, &model.DramaProduction{}, &model.DramaCast{}, &model.DramaEpisode{}, &model.DramaShot{}, &model.DramaTake{}, &model.DramaJob{}, &model.DramaSegment{}, &model.UserStorageGrant{}, &model.UserStorageState{}, &model.ResourceOffloadJob{}, &model.ReferralProfile{}, &model.ReferralBinding{}, &model.ReferralCommission{})
				}
				if history.version == 58 {
					absent = append(absent, &model.AgentMemoryProposal{}, &model.AgentMemoryRevision{}, &model.UserFeedback{})
					if err := db.Migrator().DropColumn(&model.AgentMemorySetting{}, "CompactProposalID"); err != nil {
						t.Fatal(err)
					}
				}
				if !history.walletAt58 && !history.walletAt61 {
					absent = append(absent, &model.WalletOperation{})
					if err := db.Migrator().DropColumn(&model.BillingOrder{}, "WalletMode"); err != nil {
						t.Fatal(err)
					}
				}
				for _, table := range absent {
					if err := db.Migrator().DropTable(table); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Omit("WalletMode").Create(&model.BillingOrder{ID: "existing-order", AmountMicrocredits: 12345}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&model.AgentLesson{ID: "existing-lesson", Lesson: "keep this memory"}).Error; err != nil {
					t.Fatal(err)
				}
				if history.version == 72 {
					if err := db.Create(&model.DramaProduction{ProjectID: "existing-drama", SettingsJSON: "{}"}).Error; err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Order("version").Find(&before).Error; err != nil {
					t.Fatal(err)
				}
			}
			var firstRun []schemaMigration
			for run := range 2 {
				if err := MigrateSchema(db); err != nil {
					t.Fatal(err)
				}
				if err := RequireSchemaVersion(db); err != nil {
					t.Fatal(err)
				}
				var after []schemaMigration
				if err := db.Order("version").Find(&after).Error; err != nil {
					t.Fatal(err)
				}
				if run == 0 {
					firstRun = after
				} else if !reflect.DeepEqual(firstRun, after) {
					t.Fatal("repeated migration changed applied records")
				}
			}
			for _, old := range before {
				var current schemaMigration
				if err := db.First(&current, "version = ?", old.Version).Error; err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(old, current) {
					t.Fatalf("existing migration changed: before=%+v after=%+v", old, current)
				}
			}
			for version, want := range map[int64]string{61: history.want61, 73: history.want73} {
				var record schemaMigration
				if err := db.First(&record, "version = ?", version).Error; err != nil {
					t.Fatal(err)
				}
				if record.Name != want {
					t.Fatalf("migration %d = %s, want %s", version, record.Name, want)
				}
			}
			for _, table := range []any{&model.AgentMemoryProposal{}, &model.AgentMemoryRevision{}, &model.UserFeedback{}, &model.WalletOperation{}, &model.DramaProduction{}, &model.DramaShot{}, &model.DramaJob{}, &model.DramaSegment{}, &model.UserStorageGrant{}, &model.ResourceOffloadJob{}, &model.ReferralCommission{}} {
				if !db.Migrator().HasTable(table) {
					t.Fatalf("missing merged table %T", table)
				}
			}
			if !db.Migrator().HasColumn(&model.BillingOrder{}, "WalletMode") || !db.Migrator().HasColumn(&model.AgentMemorySetting{}, "CompactProposalID") {
				t.Fatal("merged fields missing")
			}
			if history.version > 0 {
				var order model.BillingOrder
				var lesson model.AgentLesson
				if err := db.First(&order, "id = ?", "existing-order").Error; err != nil || order.AmountMicrocredits != 12345 {
					t.Fatalf("billing data lost: %+v, %v", order, err)
				}
				if err := db.First(&lesson, "id = ?", "existing-lesson").Error; err != nil || lesson.Lesson != "keep this memory" {
					t.Fatalf("memory data lost: %+v, %v", lesson, err)
				}
				if history.version == 72 {
					var drama model.DramaProduction
					if err := db.First(&drama, "project_id = ?", "existing-drama").Error; err != nil || drama.SettingsJSON != "{}" {
						t.Fatalf("drama data lost: %+v, %v", drama, err)
					}
				}
			}
			for _, version := range []int64{58, 61, 73} {
				for _, field := range []string{"checksum", "name"} {
					var original schemaMigration
					if err := db.First(&original, "version = ?", version).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.Model(&schemaMigration{}).Where("version = ?", version).Update(field, "invalid").Error; err != nil {
						t.Fatal(err)
					}
					if err := MigrateSchema(db); err == nil {
						t.Fatalf("invalid version %d %s must reject migration", version, field)
					}
					if err := RequireSchemaVersion(db); err == nil {
						t.Fatalf("invalid version %d %s must reject verification", version, field)
					}
					if err := db.Save(&original).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
