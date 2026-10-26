package database

import (
	"path/filepath"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func TestMergedSchemaPreservesBothVersion58Histories(t *testing.T) {
	for _, walletHistory := range []bool{false, true} {
		name := "main"
		if walletHistory {
			name = "shared_wallet"
		}
		t.Run(name, func(t *testing.T) {
			db, err := Open(Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "merge.db")})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })
			if err := MigrateSchema(db); err != nil {
				t.Fatal(err)
			}
			var before schemaMigration
			if err := db.First(&before, "version = ?", 58).Error; err != nil {
				t.Fatal(err)
			}
			if walletHistory {
				if err := db.Where("version >= ?", 58).Delete(&schemaMigration{}).Error; err != nil {
					t.Fatal(err)
				}
				before = schemaMigration{Version: 58, Name: "shared_wallet_billing", Checksum: "sha256:shared-wallet-billing-v58-20261019", AppliedAt: time.Now().UTC()}
				if err := db.Create(&before).Error; err != nil {
					t.Fatal(err)
				}
				for _, table := range []any{&model.AgentMemoryProposal{}, &model.AgentMemoryRevision{}, &model.UserFeedback{}} {
					if err := db.Migrator().DropTable(table); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Migrator().DropColumn(&model.AgentMemorySetting{}, "CompactProposalID"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := db.Where("version = ?", 61).Delete(&schemaMigration{}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Migrator().DropTable(&model.WalletOperation{}); err != nil {
					t.Fatal(err)
				}
				if err := db.Migrator().DropColumn(&model.BillingOrder{}, "WalletMode"); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := MigrateSchema(db); err != nil {
					t.Fatal(err)
				}
				if err := RequireSchemaVersion(db); err != nil {
					t.Fatal(err)
				}
			}
			var after, added schemaMigration
			if err := db.First(&after, "version = ?", 58).Error; err != nil {
				t.Fatal(err)
			}
			if before.Name != after.Name || before.Checksum != after.Checksum || !before.AppliedAt.Equal(after.AppliedAt) {
				t.Fatalf("existing migration changed: before=%+v after=%+v", before, after)
			}
			if err := db.First(&added, "version = ?", 61).Error; err != nil {
				t.Fatal(err)
			}
			want := "shared_wallet_billing"
			if walletHistory {
				want = "agent_memory_proposals"
			}
			if added.Name != want {
				t.Fatalf("migration 61 = %s, want %s", added.Name, want)
			}
			for _, table := range []any{&model.AgentMemoryProposal{}, &model.AgentMemoryRevision{}, &model.UserFeedback{}, &model.WalletOperation{}} {
				if !db.Migrator().HasTable(table) {
					t.Fatalf("missing merged table %T", table)
				}
			}
			if !db.Migrator().HasColumn(&model.BillingOrder{}, "WalletMode") || !db.Migrator().HasColumn(&model.AgentMemorySetting{}, "CompactProposalID") {
				t.Fatal("merged fields missing")
			}
			if err := db.Model(&schemaMigration{}).Where("version = ?", 58).Update("checksum", "invalid").Error; err != nil {
				t.Fatal(err)
			}
			if err := MigrateSchema(db); err == nil {
				t.Fatal("invalid version 58 checksum must reject migration")
			}
		})
	}
}
