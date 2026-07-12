package database

import (
	"log/slog"
	"os"

	"simnexus-go/config"
	"simnexus-go/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	glogger "gorm.io/gorm/logger"
)

// DB 是全局 GORM 数据库句柄，由 Init 初始化后供所有包直接使用。
var DB *gorm.DB

// Init 打开 SQLite 数据库并对所有 model 执行 AutoMigrate。
// AutoMigrate 只新建表或增加列，不修改/删除已有列（与 Python create_all 行为一致）。
func Init(cfg *config.Config) {
	db, err := gorm.Open(sqlite.Open(cfg.SQLitePath()), &gorm.Config{
		Logger: glogger.Default.LogMode(glogger.Silent),
	})
	if err != nil {
		slog.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	DB = db

	// AutoMigrate 只负责建新表 / 加新列，不修改已有列（与 Python create_all 行为一致）。
	// SQLite 不支持改列，GORM 尝试时会报错，忽略即可。
	_ = db.AutoMigrate(
		&models.User{},
		&models.Role{},
		&models.Modem{},
		&models.SimAccessRequest{},
		&models.SimGrant{},
		&models.SmsMessage{},
		&models.SmsScheduledTask{},
		&models.SmsTemplate{},
		&models.Notification{},
		&models.SupportMessage{},
		&models.TelegramMessage{},
	)
	// Contact 是新表，单独 AutoMigrate，避免被上面链式调用中途报错中断而漏建表。
	_ = db.AutoMigrate(&models.Contact{})

	// AutoMigrate 是单次调用：靠前的 model 在 SQLite 上尝试改列会报错并中断整个链，
	// 导致靠后 model 的新列加不上。这里对确实需要的新列显式补 ALTER（幂等，列已存在时忽略）。
	ensureColumns(db)

	slog.Info("database ready", "path", cfg.SQLitePath())
}

// ensureColumns 幂等地补齐 AutoMigrate 可能漏加的新列（SQLite ADD COLUMN，列已存在时报错忽略）。
func ensureColumns(db *gorm.DB) {
	alters := []string{
		`ALTER TABLE sms_messages ADD COLUMN channel VARCHAR(16) DEFAULT 'cellular'`,
		`ALTER TABLE modems ADD COLUMN vowifi_mode numeric DEFAULT 0`,
		`ALTER TABLE modems ADD COLUMN vowifi_epdg_ip VARCHAR(64) DEFAULT ''`,
		`ALTER TABLE modems ADD COLUMN vowifi_at_port VARCHAR(64) DEFAULT ''`,
		`ALTER TABLE modems ADD COLUMN vowifi_airplane numeric DEFAULT 1`,
	}
	for _, sql := range alters {
		_ = db.Exec(sql).Error // 列已存在会报 "duplicate column name"，忽略即可
	}
}
