package handlers

import (
	"io"
	"net/http"
	"strconv"

	"simnexus-go/config"
	"simnexus-go/database"
	"simnexus-go/models"
	"simnexus-go/security"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// TelegramListMessages godoc
// @Summary 获取Telegram消息记录
// @Tags Telegram
// @Produce json
// @Param skip query int false "偏移量"
// @Param limit query int false "每页数量"
// @Success 200 {object} handlers.R{data=[]models.TelegramMessage}
// @Security BearerAuth
// @Router /api/v1/telegram/messages [get]
func TelegramListMessages(c *gin.Context) {
	skip, _ := strconv.Atoi(c.DefaultQuery("skip", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	var msgs []models.TelegramMessage
	database.DB.Order("created_at desc").Offset(skip).Limit(limit).Find(&msgs)
	OK(c, msgs)
}

type telegramSend struct {
	Text   string `json:"text"`
	ChatID string `json:"chat_id"`
}

// TelegramSend godoc
// @Summary 发送Telegram文字消息
// @Tags Telegram
// @Accept json
// @Produce json
// @Param body body telegramSend true "消息内容"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/telegram/send [post]
func TelegramSend(c *gin.Context) {
	var body telegramSend
	c.ShouldBindJSON(&body)
	if body.Text == "" {
		Fail(c, http.StatusBadRequest, 400, "消息不能为空")
		return
	}
	if !services.TelegramSendMessage(body.Text, body.ChatID, false) {
		Fail(c, http.StatusBadGateway, 502, "发送失败，请检查 Bot Token 和 Chat ID")
		return
	}
	chatID := body.ChatID
	if chatID == "" {
		chatID = config.C.TelegramChatID
	}
	un := "SimNexus"
	database.DB.Create(&models.TelegramMessage{
		ChatID: chatID, Username: &un, Direction: "out", Text: body.Text,
	})
	OK(c, gin.H{"ok": true})
}

// TelegramSendFile godoc
// @Summary 向Telegram发送图片或文件
// @Tags Telegram
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "文件"
// @Param caption formData string false "说明文字"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/telegram/send-file [post]
func TelegramSendFile(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		Fail(c, http.StatusBadRequest, 400, "缺少文件")
		return
	}
	caption := c.PostForm("caption")
	f, err := file.Open()
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "读取失败")
		return
	}
	defer f.Close()
	content, _ := io.ReadAll(f)
	ct := file.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	ok, fileType, fileID, errMsg := services.TelegramSendFile(file.Filename, content, ct, caption)
	if !ok {
		Fail(c, http.StatusBadGateway, 502, errMsg)
		return
	}
	label := caption
	if label == "" {
		label = file.Filename
	}
	un := "SimNexus"
	ftCopy := fileType
	fidCopy := fileID
	database.DB.Create(&models.TelegramMessage{
		ChatID: config.C.TelegramChatID, Username: &un, Direction: "out",
		Text: label, FileType: &ftCopy, FileID: &fidCopy,
	})
	OK(c, gin.H{"ok": true})
}

// TelegramClearMessages godoc
// @Summary 清空Telegram消息记录
// @Tags Telegram
// @Produce json
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/telegram/messages [delete]
func TelegramClearMessages(c *gin.Context) {
	database.DB.Where("1 = 1").Delete(&models.TelegramMessage{})
	OK(c, gin.H{"ok": true})
}

// TelegramProxyFile godoc
// @Summary 代理下载Telegram文件
// @Tags Telegram
// @Param file_id path string true "Telegram file_id"
// @Param token query string true "JWT令牌"
// @Success 200 {file} binary
// @Router /api/v1/telegram/file/{file_id} [get]
func TelegramProxyFile(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		Fail(c, http.StatusUnauthorized, 401, "Not authenticated")
		return
	}
	username, err := security.ParseToken(token)
	if err != nil {
		Fail(c, http.StatusUnauthorized, 401, "Invalid token")
		return
	}
	user, err := security.LoadUserByUsername(database.DB, username)
	if err != nil || !user.IsAdmin() {
		Fail(c, http.StatusForbidden, 403, "Forbidden")
		return
	}
	if config.C.TelegramBotToken == "" {
		Fail(c, http.StatusServiceUnavailable, 503, "Bot not configured")
		return
	}
	fileID := c.Param("file_id")
	if len(fileID) > 0 && fileID[0] == '/' {
		fileID = fileID[1:]
	}
	content, ct, filename, err := services.TelegramProxyFile(fileID)
	if err != nil {
		Fail(c, http.StatusNotFound, 404, "File not found")
		return
	}
	c.Header("Content-Disposition", `inline; filename="`+filename+`"`)
	c.Data(http.StatusOK, ct, content)
}

// TelegramConfig godoc
// @Summary 获取Bot配置状态
// @Tags Telegram
// @Produce json
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/telegram/config [get]
func TelegramConfig(c *gin.Context) {
	OK(c, gin.H{
		"bot_token_set": config.C.TelegramBotToken != "",
		"chat_id":       config.C.TelegramChatID,
		"polling":       config.C.TelegramBotToken != "",
	})
}
