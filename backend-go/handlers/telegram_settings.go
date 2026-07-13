package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"simnexus-go/database"
	"simnexus-go/middleware"
	"simnexus-go/models"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// Telegram 可视化设置 + 自定义命令（均仅管理员）。

// GetTelegramSettings 返回当前有效配置状态（token 不回传明文）。
func GetTelegramSettings(c *gin.Context) {
	OK(c, services.TelegramStatus())
}

// GenTelegramBindCode 为当前登录用户生成一次性 Telegram 绑定码（任意登录用户可用）。
func GenTelegramBindCode(c *gin.Context) {
	me := middleware.CurrentUser(c)
	code := services.TelegramGenBindCode(me.ID)
	OK(c, gin.H{"code": code, "expires_in": 600})
}

// ListMyTelegramBinds 列出绑定到当前账号的所有 Telegram chat（含尽力解析的用户名）。
func ListMyTelegramBinds(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var binds []models.TelegramBind
	database.DB.Where("user_id = ?", me.ID).Order("created_at desc").Find(&binds)

	type item struct {
		ID        uint   `json:"id"`
		ChatID    string `json:"chat_id"`
		Username  string `json:"username"`
		CreatedAt string `json:"created_at"`
	}
	out := make([]item, 0, len(binds))
	for _, b := range binds {
		uname := ""
		var msg models.TelegramMessage
		if database.DB.Where("chat_id = ? AND username IS NOT NULL AND username <> ''", b.ChatID).
			Order("created_at desc").First(&msg).Error == nil && msg.Username != nil {
			uname = *msg.Username
		}
		out = append(out, item{ID: b.ID, ChatID: b.ChatID, Username: uname, CreatedAt: b.CreatedAt.Format("2006-01-02 15:04")})
	}
	OK(c, out)
}

// DeleteMyTelegramBind 解绑当前账号名下的某个 Telegram 绑定。
func DeleteMyTelegramBind(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	res := database.DB.Where("id = ? AND user_id = ?", id, me.ID).Delete(&models.TelegramBind{})
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, 404, "绑定不存在")
		return
	}
	OK(c, gin.H{"deleted": res.RowsAffected})
}

type tgSettingsIn struct {
	BotToken       string `json:"bot_token"` // 空=保留原 token
	PushChatID     string `json:"push_chat_id"`
	AllowedChatIDs string `json:"allowed_chat_ids"`
}

// SaveTelegramSettings 保存配置（校验 token）并热重启长轮询。
func SaveTelegramSettings(c *gin.Context) {
	var body tgSettingsIn
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, 400, "参数错误")
		return
	}
	if err := services.SaveTelegramSettings(body.BotToken, body.PushChatID, body.AllowedChatIDs); err != nil {
		Fail(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	OK(c, services.TelegramStatus())
}

// ---- 自定义命令 CRUD ----

func ListTelegramCommands(c *gin.Context) {
	var cmds []models.TelegramCommand
	database.DB.Order("command asc").Find(&cmds)
	OK(c, cmds)
}

type tgCommandIn struct {
	Command     string `json:"command"`
	Type        string `json:"type"`
	ReplyText   string `json:"reply_text"`
	ModemID     uint   `json:"modem_id"`
	Content     string `json:"content"`
	ToNumber    string `json:"to_number"`
	WebhookURL  string `json:"webhook_url"`
	Enabled     *bool  `json:"enabled"`
	Description string `json:"description"`
}

func (in *tgCommandIn) apply(cmd *models.TelegramCommand) error {
	name := strings.TrimSpace(strings.TrimPrefix(in.Command, "/"))
	if name == "" {
		return fmt.Errorf("命令名不能为空")
	}
	typ := in.Type
	if typ != "reply" && typ != "send" && typ != "webhook" {
		typ = "reply"
	}
	cmd.Command = name
	cmd.Type = typ
	cmd.ReplyText = in.ReplyText
	cmd.ModemID = in.ModemID
	cmd.Content = in.Content
	cmd.ToNumber = strings.TrimSpace(in.ToNumber)
	cmd.WebhookURL = strings.TrimSpace(in.WebhookURL)
	cmd.Description = strings.TrimSpace(in.Description)
	if in.Enabled != nil {
		cmd.Enabled = *in.Enabled
	}
	return nil
}

func CreateTelegramCommand(c *gin.Context) {
	var body tgCommandIn
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, 400, "参数错误")
		return
	}
	cmd := models.TelegramCommand{Enabled: true}
	if err := body.apply(&cmd); err != nil {
		Fail(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if err := database.DB.Create(&cmd).Error; err != nil {
		Fail(c, http.StatusInternalServerError, 500, "创建失败")
		return
	}
	OK(c, cmd)
}

func UpdateTelegramCommand(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var cmd models.TelegramCommand
	if database.DB.First(&cmd, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "命令不存在")
		return
	}
	var body tgCommandIn
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, 400, "参数错误")
		return
	}
	if err := body.apply(&cmd); err != nil {
		Fail(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if err := database.DB.Save(&cmd).Error; err != nil {
		Fail(c, http.StatusInternalServerError, 500, "保存失败")
		return
	}
	OK(c, cmd)
}

func DeleteTelegramCommand(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	res := database.DB.Delete(&models.TelegramCommand{}, id)
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, 404, "命令不存在")
		return
	}
	OK(c, gin.H{"deleted": res.RowsAffected})
}
