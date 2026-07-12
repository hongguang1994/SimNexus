package models

import "time"

// Contact 是用户私有通讯录里的一条联系人（按 OwnerID 隔离，互不可见）。
type Contact struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	OwnerID   uint      `gorm:"not null;index" json:"owner_id"` // 归属用户
	Name      string    `gorm:"type:text;not null;default:''" json:"name"`
	Phone     string    `gorm:"type:text;not null;default:''" json:"phone"`
	Company   string    `gorm:"type:text;not null;default:''" json:"company"`
	Note      string    `gorm:"type:text;not null;default:''" json:"note"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Contact) TableName() string { return "contacts" }
