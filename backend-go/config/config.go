package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config 保存从环境变量 / .env 文件加载的运行时配置。
type Config struct {
	AppName          string   // 应用名称
	DatabaseURL      string   // SQLAlchemy 风格的数据库 URL
	SecretKey        string   // JWT 签名密钥，生产环境必须修改
	ModemPollSeconds int      // 调制解调器轮询间隔（秒）
	CorsOrigins      []string // 允许的 CORS 来源列表
	TelegramBotToken     string   // Telegram Bot Token
	TelegramChatID       string   // Telegram 推送目标 Chat ID
	TelegramAllowedChats []string // 允许发命令的 chat_id 白名单（含 TelegramChatID）；空则仅 TelegramChatID
	UploadDir            string   // 文件上传存储目录
}

// C 是全局配置单例，由 Load() 初始化后可在任意包中访问。
var C *Config

// SecretKey 与 Algorithm 镜像 Python 安全模块的常量，供 JWT 签名使用。
var SecretKey string

// Algorithm 指定 JWT 签名算法（HS256）。
const Algorithm = "HS256"

// Load 从 .env（如存在）和环境变量读取配置，并将结果写入全局 C。
func Load() *Config {
	_ = godotenv.Load(".env")

	cfg := &Config{
		AppName:          getenv("APP_NAME", "SimNexus"),
		DatabaseURL:      getenv("DATABASE_URL", "sqlite:///./sim_manager.db"),
		SecretKey:        getenv("SECRET_KEY", "simnexus-secret-key-change-in-production"),
		ModemPollSeconds: getenvInt("MODEM_POLL_INTERVAL", 10),
		TelegramBotToken: getenv("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatID:   getenv("TELEGRAM_CHAT_ID", ""),
		UploadDir:        getenv("UPLOAD_DIR", "/opt/simnexus/uploads"),
	}

	origins := getenv("CORS_ORIGINS", "http://localhost:3000,http://localhost:5173")
	for _, o := range strings.Split(origins, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			cfg.CorsOrigins = append(cfg.CorsOrigins, o)
		}
	}

	// Telegram 命令白名单：TELEGRAM_ALLOWED_CHAT_IDS（逗号分隔）+ 默认推送 chat_id。
	// 只有白名单内的 chat 能对 Bot 发命令，防止任何人搜到 Bot 就能读短信/用卡发短信。
	seen := map[string]bool{}
	addChat := func(id string) {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			cfg.TelegramAllowedChats = append(cfg.TelegramAllowedChats, id)
		}
	}
	addChat(cfg.TelegramChatID)
	for _, id := range strings.Split(getenv("TELEGRAM_ALLOWED_CHAT_IDS", ""), ",") {
		addChat(id)
	}

	C = cfg
	SecretKey = cfg.SecretKey
	return cfg
}

// SQLitePath 将 SQLAlchemy 风格的 URL（如 sqlite:///./foo.db）转换为纯文件路径。
func (c *Config) SQLitePath() string {
	u := c.DatabaseURL
	u = strings.TrimPrefix(u, "sqlite:///")
	u = strings.TrimPrefix(u, "sqlite://")
	if u == "" {
		return "sim_manager.db"
	}
	return u
}

// getenv 读取环境变量，若未设置则返回默认值。
func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getenvInt 读取整数类型的环境变量，解析失败时返回默认值。
func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
