package models

import "time"

// TelegramBind 把一个 Telegram chat_id 绑定到一个 SimNexus 账号，
// 使 /contacts 等命令按用户返回其自己的数据。一个 chat 绑一个账号。
type TelegramBind struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ChatID    string    `gorm:"type:text;not null;uniqueIndex" json:"chat_id"`
	UserID    uint      `gorm:"not null;index" json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (TelegramBind) TableName() string { return "telegram_binds" }
