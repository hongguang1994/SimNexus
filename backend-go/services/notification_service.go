package services

import (
	"simnexus-go/models"
	"simnexus-go/security"

	"gorm.io/gorm"
)

// NotificationService 提供通知相关的数据库查询和更新操作。
type NotificationService struct {
	db *gorm.DB
}

// NewNotificationService 创建 NotificationService 实例。
func NewNotificationService(db *gorm.DB) *NotificationService {
	return &NotificationService{db: db}
}

// visibleFilter 对通知查询追加按 audience 过滤的 WHERE 条件。
// audience 规则：
//   - "all"     → 所有认证用户均可见
//   - "user"    → 仅 target_user_id 对应的用户可见
//   - "admin"   → 仅管理员可见
//   - "support" → 管理员 + 拥有 can_support 角色的用户可见
func (s *NotificationService) visibleFilter(me *models.User, q *gorm.DB) *gorm.DB {
	// 基础条件：audience='all' 或 audience='user' 且 target=当前用户
	cond := "audience = 'all' OR (audience = 'user' AND target_user_id = ?)"
	args := []interface{}{me.ID}
	if me.IsAdmin() {
		// 管理员可见 admin 和 support 通知
		cond += " OR audience = 'admin' OR audience = 'support'"
	} else if security.IsSupportStaff(me) {
		// 客服人员可见 support 通知
		cond += " OR audience = 'support'"
	}
	return q.Where(cond, args...)
}

// ListNotifications 查询对当前用户可见的通知列表，按 ID 倒序，最多返回 limit 条。
func (s *NotificationService) ListNotifications(me *models.User, limit int) ([]models.Notification, error) {
	if limit > 100 {
		limit = 100
	}
	var ns []models.Notification
	err := s.visibleFilter(me, s.db.Model(&models.Notification{})).
		Order("id desc").Limit(limit).Find(&ns).Error
	return ns, err
}

// UnreadCount 统计当前用户未读通知数量。
func (s *NotificationService) UnreadCount(me *models.User) (int64, error) {
	var count int64
	err := s.visibleFilter(me, s.db.Model(&models.Notification{})).
		Where("is_read = ?", false).Count(&count).Error
	return count, err
}

// MarkAllRead 将当前用户所有未读通知标为已读。
func (s *NotificationService) MarkAllRead(me *models.User) error {
	return s.visibleFilter(me, s.db.Model(&models.Notification{})).
		Where("is_read = ?", false).Update("is_read", true).Error
}

// MarkOneRead 将指定通知标为已读（只操作对当前用户可见的通知）。
func (s *NotificationService) MarkOneRead(me *models.User, id uint) error {
	var n models.Notification
	if s.visibleFilter(me, s.db.Model(&models.Notification{})).
		Where("id = ?", id).First(&n).Error == nil {
		n.IsRead = true
		s.db.Save(&n)
	}
	// 通知不存在时静默返回，不报错（幂等操作）
	return nil
}
