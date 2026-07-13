package models

import "time"

// TelegramConfig 是 Telegram Bot 的可视化配置（单行，id=1）。为空的字段回退到环境变量。
type TelegramConfig struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	BotToken       string    `gorm:"type:text;not null;default:''" json:"bot_token"`
	PushChatID     string    `gorm:"type:text;not null;default:''" json:"push_chat_id"`
	AllowedChatIDs string    `gorm:"type:text;not null;default:''" json:"allowed_chat_ids"` // 逗号分隔
	UpdatedAt      time.Time `json:"updated_at"`
}

func (TelegramConfig) TableName() string { return "telegram_config" }

// TelegramCommand 是用户自定义的 Bot 命令。
//   - Type="reply"：收到 /Command 回复固定文本 ReplyText
//   - Type="send" ：收到 /Command 用 ModemID（0=自动选单卡）发一条 Content 短信；
//     收件号码取命令参数，无参数则用预设 ToNumber
//   - Type="webhook"：收到 /Command 把 {command,args,text,chat_id} POST 到 WebhookURL，
//     用返回体（或 JSON 的 text 字段）作为回复。任意逻辑写在外部服务，无需改后台。
type TelegramCommand struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Command     string    `gorm:"type:text;not null;default:''" json:"command"` // 不含前导 /
	Type        string    `gorm:"type:text;not null;default:'reply'" json:"type"`
	ReplyText   string    `gorm:"type:text;not null;default:''" json:"reply_text"`
	ModemID     uint      `gorm:"not null;default:0" json:"modem_id"` // 0=自动
	Content     string    `gorm:"type:text;not null;default:''" json:"content"`
	ToNumber    string    `gorm:"type:text;not null;default:''" json:"to_number"` // 预设号码，空=需参数
	WebhookURL  string    `gorm:"type:text;not null;default:''" json:"webhook_url"`
	Enabled     bool      `gorm:"not null;default:true" json:"enabled"`
	Description string    `gorm:"type:text;not null;default:''" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (TelegramCommand) TableName() string { return "telegram_commands" }
