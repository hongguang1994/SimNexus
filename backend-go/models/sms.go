package models

import "time"

// 短信方向常量。
const (
	SmsInbound  = "inbound"  // 收件
	SmsOutbound = "outbound" // 发件
)

// 短信状态常量。
const (
	SmsPending  = "pending"  // 待发送
	SmsSent     = "sent"     // 发送成功
	SmsFailed   = "failed"   // 发送失败
	SmsReceived = "received" // 已收到（入站）
)

// 短信收发渠道常量。
const (
	SmsChannelCellular = "cellular" // 走 ModemManager/mmcli（蜂窝短信）
	SmsChannelVowifi   = "vowifi"   // 走自建 VoWiFi 协议栈（IMS over Wi-Fi）
)

// 定时任务状态常量。
const (
	TaskActive    = "active"    // 运行中
	TaskPaused    = "paused"    // 已暂停
	TaskCompleted = "completed" // 一次性任务已完成
	TaskFailed    = "failed"    // 全部收件人发送失败
)

// SmsMessage 记录一条短信（收件或发件）。
// 收件去重键为 (modem_id, mm_sms_index, direction=inbound)。
type SmsMessage struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	ModemID      uint       `gorm:"not null" json:"modem_id"`
	Direction    string     `gorm:"size:16;not null" json:"direction"`       // inbound/outbound
	Channel      string     `gorm:"size:16;default:cellular" json:"channel"` // 收发渠道：cellular / vowifi
	PhoneNumber  string     `gorm:"size:30;not null" json:"phone_number"`    // 对端号码
	Content      string     `gorm:"type:text;not null" json:"content"`
	Status       string     `gorm:"size:16;default:pending" json:"status"`
	ErrorMessage *string    `gorm:"type:text" json:"error_message"` // 发送失败原因
	SentAt       *time.Time `json:"sent_at"`
	ReceivedAt   *time.Time `json:"received_at"`
	CreatedAt    time.Time  `json:"created_at"`

	MmSmsIndex      string `gorm:"column:mm_sms_index;size:20" json:"mm_sms_index"` // mmcli SMS 对象索引，用于去重
	ScheduledTaskID *uint  `json:"scheduled_task_id"`                               // 关联的定时任务（手动发送时为 nil）
	CreatedByID     *uint  `json:"created_by_id"`                                   // 发送人
}

func (SmsMessage) TableName() string { return "sms_messages" }

// SmsScheduledTask 定时短信任务，支持 cron 表达式或一次性发送。
// SendOnceAt 存储 UTC 时间，前端提交前须用 .toISOString() 转换。
// 一次性任务执行后状态变为 completed（部分失败）或 failed（全部失败）。
type SmsScheduledTask struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	Name           string     `gorm:"size:100;not null" json:"name"`
	ModemID        uint       `gorm:"not null" json:"modem_id"`
	Recipients     JSONList   `gorm:"type:json;not null" json:"recipients"` // 收件人号码列表
	Content        string     `gorm:"type:text;not null" json:"content"`
	CronExpression *string    `gorm:"size:100" json:"cron_expression"` // 与 SendOnceAt 二选一
	SendOnceAt     *time.Time `json:"send_once_at"`                    // 一次性发送时间（UTC）
	Status         string     `gorm:"size:16;default:active" json:"status"`
	LastRunAt      *time.Time `json:"last_run_at"`
	NextRunAt      *time.Time `json:"next_run_at"`
	RunCount       int        `gorm:"default:0" json:"run_count"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CreatedByID    *uint      `json:"created_by_id"`
}

func (SmsScheduledTask) TableName() string { return "sms_scheduled_tasks" }
