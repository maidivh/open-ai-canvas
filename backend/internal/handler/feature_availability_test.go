package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestFeatureAvailabilityPartialPatchKeepsInternalPolicyAcrossModeChange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.User{}, &model.AuthSession{}, &model.SystemSetting{}, &model.AdminAuditEvent{},
		&model.BillingOrder{}, &model.WalletOperation{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.User{ID: "admin", Username: "admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive},
		&model.AuthSession{ID: "admin", UserID: "admin", TokenHash: auth.HashToken("test-token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.SystemSetting{Key: "feature_availability", ValueJSON: `{"creditsEnabled":false,"welcomeEnabled":true}`},
		&model.SystemSetting{Key: model.SharedWalletSettingKey, ValueJSON: `{"mode":"external","version":1}`},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := repository.New(db)
	switched := false
	const callback = "feature-patch:switch-after-admin-read"
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if switched || !strings.Contains(tx.Statement.SQL.String(), "system_settings") {
			return
		}
		for _, value := range tx.Statement.Vars {
			if value == model.SharedWalletSettingKey {
				switched = true
				if err := repo.SaveSharedWalletSettings(model.SharedWalletSettings{Mode: "internal"}, 1); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Query().Remove(callback) })
	svc := service.New(repo, t.TempDir())
	router := gin.New()
	RegisterFeatureAvailabilityRoutes(router.Group("/api"), svc)
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/settings/features", strings.NewReader(`{"welcomeEnabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "admin.test-token"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("partial feature patch failed: %d %s", w.Code, w.Body.String())
	}
	if !switched {
		t.Fatal("mode did not change between admin read and partial update")
	}
	setting, err := repo.SystemSetting("feature_availability")
	if err != nil {
		t.Fatal(err)
	}
	var persisted service.FeatureAvailability
	if err := json.Unmarshal([]byte(setting.ValueJSON), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.CreditsEnabled || persisted.WelcomeEnabled {
		t.Fatalf("partial welcome patch changed internal credits: %+v", persisted)
	}
}
