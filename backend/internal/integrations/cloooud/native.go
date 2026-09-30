package cloooud

import (
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

// NativeHost 由宿主注入能力；模块不复制原生会话生成算法。
type NativeHost struct {
	CurrentUser       func(string) (*model.User, error)
	CreateSession     func(*model.User) (*auth.AuthSessionResult, error)
	EnsureSignupBonus func(string) error
	RecordActivity    func(string, string, int)
}

func (s *Service) completeNativeLogin(userID string) (*auth.AuthSessionResult, error) {
	user, err := s.repo.User(userID)
	if err != nil {
		return nil, err
	}
	if user.Status != model.UserStatusActive {
		return nil, kernel.Forbidden("该影策账号已被禁用")
	}
	if err := s.auth.EnsureSignupBonus(user.ID); err != nil {
		return nil, err
	}
	now := time.Now()
	if err := s.recordActiveUserLogin(user.ID, now); err != nil {
		return nil, err
	}
	user.LastLoginAt = &now
	user.UpdatedAt = now
	result, err := s.auth.CreateSession(user)
	if err != nil {
		return nil, err
	}
	s.auth.RecordActivity(user.ID, "login", 1)
	return result, nil
}

// 仅更新仍有效用户的时间，避免覆盖管理员刚修改的权限或封禁状态。
func (s *Service) recordActiveUserLogin(userID string, at time.Time) error {
	result := s.db.Model(&model.User{}).Where("id = ? AND status = ?", userID, model.UserStatusActive).
		Updates(map[string]any{"last_login_at": at, "updated_at": at})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
