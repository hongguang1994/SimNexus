package services

import (
	"strings"
	"time"

	"simnexus-go/models"
	"simnexus-go/security"

	"gorm.io/gorm"
)

// SupportService 提供客服消息相关的数据库操作。
type SupportService struct {
	db *gorm.DB
}

// NewSupportService 创建 SupportService 实例。
func NewSupportService(db *gorm.DB) *SupportService {
	return &SupportService{db: db}
}

// MsgOut 将消息模型格式化为 API 响应，包含发送人用户名。
func (s *SupportService) MsgOut(m *models.SupportMessage) map[string]interface{} {
	var sender models.User
	name := "?"
	if s.db.First(&sender, m.SenderID).Error == nil {
		name = sender.Username
	}
	return map[string]interface{}{
		"id":              m.ID,
		"user_id":         m.UserID,
		"sender_id":       m.SenderID,
		"sender_name":     name,
		"content":         m.Content,
		"is_from_user":    m.IsFromUser,
		"is_read":         m.IsRead,
		"created_at":      m.CreatedAt,
		"attachment_url":  m.AttachmentURL,
		"attachment_name": m.AttachmentName,
		"attachment_type": m.AttachmentType,
	}
}

// SendMessageInput 发送客服消息的输入参数。
type SendMessageInput struct {
	Content        string // 文本内容（与附件至少有一个非空）
	AttachmentURL  string // 附件访问路径
	AttachmentName string // 附件原始文件名
	AttachmentType string // 附件类型（image 或 file）
	TargetUserID   *uint  // 客服发消息时必须指定目标用户 ID
}

// SendMessage 发送客服消息，自动区分用户发消息和客服回复。
// 返回创建的消息记录。
func (s *SupportService) SendMessage(me *models.User, input SendMessageInput) (*models.SupportMessage, error) {
	isStaff := security.IsSupportStaff(me)
	var msg models.SupportMessage
	if isStaff {
		// 客服回复时必须指定目标用户
		if input.TargetUserID == nil {
			return nil, ErrUserNotFound
		}
		var target models.User
		if s.db.First(&target, *input.TargetUserID).Error != nil {
			return nil, ErrUserNotFound
		}
		msg = models.SupportMessage{
			UserID: *input.TargetUserID, SenderID: me.ID,
			Content: strings.TrimSpace(input.Content), IsFromUser: false,
		}
	} else {
		// 普通用户发消息，userID 和 senderID 均为自己
		msg = models.SupportMessage{
			UserID: me.ID, SenderID: me.ID,
			Content: strings.TrimSpace(input.Content), IsFromUser: true,
		}
	}
	msg.AttachmentURL = strPtrSvc(input.AttachmentURL)
	msg.AttachmentName = strPtrSvc(input.AttachmentName)
	msg.AttachmentType = strPtrSvc(input.AttachmentType)
	msg.CreatedAt = time.Now()
	if err := s.db.Create(&msg).Error; err != nil {
		return nil, err
	}
	return &msg, nil
}

// GetMessages 获取消息记录：客服按 userID 查询；普通用户只能查看自己的会话。
// sinceID 用于增量拉取（只返回 id > sinceID 的消息）。
func (s *SupportService) GetMessages(me *models.User, userID, sinceID string) ([]models.SupportMessage, error) {
	q := s.db.Model(&models.SupportMessage{})
	if security.IsSupportStaff(me) {
		if userID == "" {
			return nil, ErrUserNotFound // handler 层应提前校验
		}
		q = q.Where("user_id = ?", userID)
	} else {
		q = q.Where("user_id = ?", me.ID)
	}
	if sinceID != "" {
		q = q.Where("id > ?", sinceID)
	}
	var msgs []models.SupportMessage
	err := q.Order("created_at asc").Find(&msgs).Error
	return msgs, err
}

// MarkRead 标记消息为已读。
// 客服操作：将指定用户发来的消息标记为已读。
// 普通用户：将客服回复标记为已读。
func (s *SupportService) MarkRead(me *models.User, userID string) error {
	if security.IsSupportStaff(me) {
		// 客服标记用户消息为已读
		return s.db.Model(&models.SupportMessage{}).
			Where("user_id = ? AND is_from_user = ? AND is_read = ?", userID, true, false).
			Update("is_read", true).Error
	}
	// 普通用户标记客服回复为已读（is_from_user=false 表示客服回复）
	return s.db.Model(&models.SupportMessage{}).
		Where("user_id = ? AND is_from_user = ? AND is_read = ?", me.ID, false, false).
		Update("is_read", true).Error
}

// UnreadCount 统计未读消息数量。
// 客服：统计所有用户发来的未读消息总数。
// 普通用户：统计自己收到的未读客服回复数。
func (s *SupportService) UnreadCount(me *models.User) (int64, error) {
	var count int64
	var err error
	if security.IsSupportStaff(me) {
		err = s.db.Model(&models.SupportMessage{}).
			Where("is_from_user = ? AND is_read = ?", true, false).Count(&count).Error
	} else {
		err = s.db.Model(&models.SupportMessage{}).
			Where("user_id = ? AND is_from_user = ? AND is_read = ?", me.ID, false, false).Count(&count).Error
	}
	return count, err
}

// Conversation 单个用户会话的摘要信息（用于客服会话列表）。
type Conversation struct {
	UserID      uint      `json:"user_id"`
	Username    string    `json:"username"`
	LastMessage string    `json:"last_message"` // 最近一条消息预览（最多 50 字符）
	LastAt      time.Time `json:"last_at"`
	UnreadCount int64     `json:"unread_count"` // 客服未读的用户消息数
}

// ListConversations 获取所有有对话记录的用户会话列表，按最近消息时间倒序。
// 仅客服权限可调用，handler 层需预先校验。
func (s *SupportService) ListConversations() ([]Conversation, error) {
	// 查询所有有消息记录的用户 ID
	var userIDs []uint
	if err := s.db.Model(&models.SupportMessage{}).Distinct("user_id").Pluck("user_id", &userIDs).Error; err != nil {
		return nil, err
	}
	out := make([]Conversation, 0, len(userIDs))
	for _, uid := range userIDs {
		var user models.User
		if s.db.First(&user, uid).Error != nil {
			continue // 用户已被删除，跳过
		}
		var last models.SupportMessage
		s.db.Where("user_id = ?", uid).Order("created_at desc").First(&last)
		var unread int64
		s.db.Model(&models.SupportMessage{}).
			Where("user_id = ? AND is_from_user = ? AND is_read = ?", uid, true, false).Count(&unread)
		// 优先使用附件名作为预览；内容截取前 50 字符
		preview := last.Content
		if last.AttachmentName != nil && *last.AttachmentName != "" {
			preview = *last.AttachmentName
		}
		if len(preview) > 50 {
			preview = preview[:50]
		}
		if last.AttachmentURL != nil && *last.AttachmentURL != "" && last.Content == "" {
			preview = "[附件] " + preview
		}
		out = append(out, Conversation{
			UserID: uid, Username: user.Username, LastMessage: preview,
			LastAt: last.CreatedAt, UnreadCount: unread,
		})
	}
	// 按最后消息时间降序排列（冒泡，数量通常不大）
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].LastAt.After(out[i].LastAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// strPtrSvc 将非空字符串转为指针，空字符串返回 nil（仅在 services 包内使用）。
func strPtrSvc(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
