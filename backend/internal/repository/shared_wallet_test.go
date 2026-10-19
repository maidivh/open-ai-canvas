package repository

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"path/filepath"
	"testing"
	"yingce/backend/internal/model"
)

func TestExternalWalletModeSwitchSerializesAcrossConnections(t *testing.T) {
	for i := 0; i < 5; i++ {
		dsn := filepath.Join(t.TempDir(), "wallet.db") + "?_journal_mode=WAL&_busy_timeout=5000"
		a, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		for _, db := range []*gorm.DB{a, b} {
			sqlDB, _ := db.DB()
			t.Cleanup(func() { sqlDB.Close() })
		}
		if err = a.AutoMigrate(&model.SystemSetting{}, &model.CreditAccount{}, &model.CreditLedgerEntry{}, &model.BillingOrder{}, &model.WalletOperation{}); err != nil {
			t.Fatal(err)
		}
		a.Create(&model.SystemSetting{Key: model.SharedWalletSettingKey, ValueJSON: `{"mode":"internal","version":0}`})
		a.Create(&model.CreditAccount{UserID: "member", AvailableMicrocredits: 10000000})
		start := make(chan struct{})
		admitted := make(chan error, 1)
		switched := make(chan error, 1)
		go func() {
			<-start
			admitted <- New(a).ReserveBillingOrder(&model.BillingOrder{ID: "bill", UserID: "member", IdempotencyKey: "bill", AmountMicrocredits: 1000000, Status: model.BillingStatusReserved, WalletMode: "internal", WalletVersion: 0})
		}()
		go func() {
			<-start
			switched <- New(b).SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 0)
		}()
		close(start)
		admitErr, switchErr := <-admitted, <-switched
		if (admitErr == nil) == (switchErr == nil) {
			t.Fatalf("mode admission not exclusive: admitted=%v switched=%v", admitErr, switchErr)
		}
		cfg, err := New(a).SharedWalletSettings()
		if err != nil {
			t.Fatal(err)
		}
		var count int64
		a.Model(&model.BillingOrder{}).Count(&count)
		if admitErr == nil && (count != 1 || cfg.Version != 0) {
			t.Fatal("admitted bill crossed config version")
		}
		if switchErr == nil && (count != 0 || cfg.Version != 1) {
			t.Fatal("stale quote admitted after mode change")
		}
	}
}

func TestExternalWalletAdmissionDoesNotSpendInternalBalance(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.SystemSetting{}, &model.CreditAccount{}, &model.CreditLedgerEntry{}, &model.BillingOrder{}, &model.UserIdentity{}); err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	if err = db.Create(&model.SystemSetting{Key: "shared_wallet", ValueJSON: `{"mode":"external","baseUrl":"https://wallet.example","clientId":"canvas","siteId":1,"issuer":"https://id.example","secretCipher":"cipher","version":1}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.CreditAccount{UserID: "unbound", AvailableMicrocredits: 9000000}).Error; err != nil {
		t.Fatal(err)
	}
	order := &model.BillingOrder{ID: "order-1", UserID: "unbound", IdempotencyKey: "admission", AmountMicrocredits: 1000000, Status: model.BillingStatusReserved}
	if err = repo.ReserveBillingOrder(order); err == nil {
		t.Fatal("external wallet admitted an unbound user and spent internal funds")
	}
	var account model.CreditAccount
	if err = db.First(&account, "user_id = ?", "unbound").Error; err != nil {
		t.Fatal(err)
	}
	if account.AvailableMicrocredits != 9000000 {
		t.Fatalf("internal funds changed: %d", account.AvailableMicrocredits)
	}
}
