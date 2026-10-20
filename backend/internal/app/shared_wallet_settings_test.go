package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestSharedWalletSettingsIdentityMismatchIsActionable(t *testing.T) {
	for _, name := range []string{"issuer_trailing_slash", "wrong_site", "no_binding"} {
		t.Run(name, func(t *testing.T) {
			s, db, up, _ := newSharedWalletTest(t)
			admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
			before, err := s.repo.SharedWalletSettings()
			if err != nil {
				t.Fatal(err)
			}
			view := walletSettingsView(before)
			switch name {
			case "issuer_trailing_slash":
				view.Issuer += "/"
			case "wrong_site":
				view.SiteID++
			case "no_binding":
				if err := db.Where("provider = ?", "cloooud").Delete(&model.UserIdentity{}).Error; err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.UpdateWalletSettings(context.Background(), admin, WalletSettingsRequest{WalletSettingsView: view})
			var appErr *AppError
			if !errors.As(err, &appErr) || appErr.Status != http.StatusBadRequest ||
				!strings.Contains(appErr.Message, "SSO") || !strings.Contains(appErr.Message, "免登") {
				t.Fatalf("expected actionable identity configuration error, got %T: %v", err, err)
			}
			after, err := s.repo.SharedWalletSettings()
			if err != nil || after != before {
				t.Fatal("identity mismatch changed wallet settings")
			}
			up.mu.Lock()
			calls := len(up.calls)
			up.mu.Unlock()
			if calls != 0 {
				t.Fatal("identity mismatch reached the remote wallet")
			}
		})
	}
}

func TestSharedWalletSettingsIdentityDatabaseFailureStaysInternal(t *testing.T) {
	s, db, _, _ := newSharedWalletTest(t)
	cfg, err := s.repo.SharedWalletSettings()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&model.UserIdentity{}); err != nil {
		t.Fatal(err)
	}
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	_, err = s.UpdateWalletSettings(context.Background(), admin, WalletSettingsRequest{WalletSettingsView: walletSettingsView(cfg)})
	var appErr *AppError
	if err == nil || errors.As(err, &appErr) {
		t.Fatalf("database failure must not be exposed as a configuration error: %v", err)
	}
}
