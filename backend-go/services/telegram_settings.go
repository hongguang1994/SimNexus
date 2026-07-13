package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"simnexus-go/config"
	"simnexus-go/database"
	"simnexus-go/models"
)

// Telegram 有效配置：优先取 DB（telegram_config 单行），字段为空则回退到环境变量。
// 支持从后台 UI 修改后热重启长轮询，无需改 env / 重新部署。

type tgSettings struct {
	token   string
	pushID  string
	allowed []string
}

var (
	tgSetMu  sync.RWMutex
	tgSetCur tgSettings

	tgPollMu     sync.Mutex
	tgPollParent context.Context
	tgPollCancel context.CancelFunc
)

// LoadTelegramSettings 从 DB 读取配置并合并环境变量兜底，刷新内存缓存。启动时与保存后调用。
func LoadTelegramSettings() {
	var row models.TelegramConfig
	database.DB.First(&row, 1)

	s := tgSettings{
		token:  firstNonEmpty(row.BotToken, config.C.TelegramBotToken),
		pushID: firstNonEmpty(row.PushChatID, config.C.TelegramChatID),
	}
	// 允许命令的 chat_id：DB 配置（逗号分隔）优先，否则用 env 解析好的白名单；始终并入推送 chat_id。
	seen := map[string]bool{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			s.allowed = append(s.allowed, id)
		}
	}
	if strings.TrimSpace(row.AllowedChatIDs) != "" {
		for _, id := range strings.Split(row.AllowedChatIDs, ",") {
			add(id)
		}
	} else {
		for _, id := range config.C.TelegramAllowedChats {
			add(id)
		}
	}
	add(s.pushID)

	tgSetMu.Lock()
	tgSetCur = s
	tgSetMu.Unlock()
}

func tgSet() tgSettings {
	tgSetMu.RLock()
	defer tgSetMu.RUnlock()
	return tgSetCur
}

// TelegramValidateToken 调 getMe 校验 token，返回 bot 用户名。
func TelegramValidateToken(token string) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("token 为空")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/bot%s/getMe", telegramAPIBase, token))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var data struct {
		Ok     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if json.Unmarshal(body, &data) != nil || !data.Ok {
		return "", fmt.Errorf("token 无效")
	}
	return data.Result.Username, nil
}

// SaveTelegramSettings 持久化配置（token 传空表示保留原值），校验后刷新缓存并热重启长轮询。
func SaveTelegramSettings(token, pushID, allowed string) error {
	var row models.TelegramConfig
	database.DB.First(&row, 1)
	row.ID = 1
	if strings.TrimSpace(token) != "" {
		if _, err := TelegramValidateToken(token); err != nil {
			return fmt.Errorf("Bot Token 校验失败：%v", err)
		}
		row.BotToken = strings.TrimSpace(token)
	}
	row.PushChatID = strings.TrimSpace(pushID)
	row.AllowedChatIDs = strings.TrimSpace(allowed)
	if err := database.DB.Save(&row).Error; err != nil {
		return err
	}
	LoadTelegramSettings()
	RestartTelegramPolling()
	return nil
}

// TelegramStatus 供前端展示的当前状态（token 不回传明文）。
func TelegramStatus() map[string]any {
	s := tgSet()
	username := ""
	if s.token != "" {
		username, _ = TelegramValidateToken(s.token)
	}
	return map[string]any{
		"has_token":        s.token != "",
		"bot_username":     username,
		"push_chat_id":     s.pushID,
		"allowed_chat_ids": strings.Join(s.allowed, ","),
		"polling":          s.token != "",
	}
}

// RestartTelegramPolling 取消旧的长轮询协程并用当前 token 重新启动（token 变更后需换 Bot）。
func RestartTelegramPolling() {
	tgPollMu.Lock()
	defer tgPollMu.Unlock()
	if tgPollCancel != nil {
		tgPollCancel()
		tgPollCancel = nil
	}
	if tgPollParent == nil || tgSet().token == "" {
		return
	}
	ctx, cancel := context.WithCancel(tgPollParent)
	tgPollCancel = cancel
	tgLastUpdateID = 0 // 换 Bot 后 offset 归零
	go tgPollLoop(ctx)
}
