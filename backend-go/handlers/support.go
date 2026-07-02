package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"simnexus-go/config"
	"simnexus-go/database"
	"simnexus-go/middleware"
	"simnexus-go/security"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// maxSupportFileSize 客服附件上传大小上限（20MB）。
const maxSupportFileSize = 20 * 1024 * 1024

// supportImageTypes 支持内联预览的图片 MIME 类型。
var supportImageTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true,
	"image/webp": true, "image/bmp": true,
}

// SupportUpload godoc
// @Summary 上传客服附件
// @Tags 客服
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "附件"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/support/upload [post]
func SupportUpload(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		Fail(c, http.StatusBadRequest, 400, "缺少文件")
		return
	}
	if file.Size > maxSupportFileSize {
		Fail(c, http.StatusBadRequest, 400, "文件大小超过 20MB 限制")
		return
	}
	os.MkdirAll(config.C.UploadDir, 0o755)
	// 生成随机文件名防止路径遍历，保留原始扩展名
	ext := strings.ToLower(filepath.Ext(file.Filename))
	buf := make([]byte, 16)
	rand.Read(buf)
	name := hex.EncodeToString(buf) + ext
	dst := filepath.Join(config.C.UploadDir, name)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		Fail(c, http.StatusInternalServerError, 500, "保存失败")
		return
	}
	ct := file.Header.Get("Content-Type")
	attType := "file"
	if supportImageTypes[ct] {
		attType = "image"
	}
	OK(c, gin.H{"url": "/api/support/files/" + name, "name": file.Filename, "type": attType})
}

// SupportServeFile godoc
// @Summary 下载客服附件
// @Tags 客服
// @Param filename path string true "文件名"
// @Success 200 {file} binary
// @Router /api/v1/support/files/{filename} [get]
func SupportServeFile(c *gin.Context) {
	filename := c.Param("filename")
	// 防止路径遍历攻击
	if strings.Contains(filename, "/") || strings.Contains(filename, "..") {
		Fail(c, http.StatusBadRequest, 400, "非法文件名")
		return
	}
	path := filepath.Join(config.C.UploadDir, filename)
	if _, err := os.Stat(path); err != nil {
		Fail(c, http.StatusNotFound, 404, "文件不存在")
		return
	}
	c.File(path)
}

// messageIn 发送客服消息的请求体。
type messageIn struct {
	Content        string `json:"content"          binding:"omitempty,max=2000"`
	UserID         *uint  `json:"user_id"`                                          // 客服发消息时必填（目标用户）
	AttachmentURL  string `json:"attachment_url"   binding:"omitempty,max=500"`
	AttachmentName string `json:"attachment_name"  binding:"omitempty,max=255"`
	AttachmentType string `json:"attachment_type"  binding:"omitempty,oneof=image file"` // image 或 file
}

// SupportSendMessage godoc
// @Summary 发送客服消息
// @Tags 客服
// @Accept json
// @Produce json
// @Param body body messageIn true "消息内容"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/support/messages [post]
func SupportSendMessage(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var body messageIn
	c.ShouldBindJSON(&body)
	if strings.TrimSpace(body.Content) == "" && body.AttachmentURL == "" {
		Fail(c, http.StatusBadRequest, 400, "消息或附件不能同时为空")
		return
	}
	svc := services.NewSupportService(database.DB)
	msg, err := svc.SendMessage(me, services.SendMessageInput{
		Content:        body.Content,
		AttachmentURL:  body.AttachmentURL,
		AttachmentName: body.AttachmentName,
		AttachmentType: body.AttachmentType,
		TargetUserID:   body.UserID,
	})
	if err != nil {
		Fail(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	// 推送通知：客服回复通知用户；用户消息通知客服
	preview := previewText(body.Content, body.AttachmentName)
	if security.IsSupportStaff(me) {
		services.Push("support_reply", "客服已回复您的咨询", preview, "user", body.UserID)
	} else {
		services.Push("support_msg", "用户咨询："+me.Username, preview, "support", nil)
	}
	OK(c, svc.MsgOut(msg))
}

// SupportGetMessages godoc
// @Summary 获取客服消息记录
// @Tags 客服
// @Produce json
// @Param user_id query int false "用户ID（客服用）"
// @Param since_id query int false "增量拉取起始ID"
// @Success 200 {object} handlers.R{data=[]models.SupportMessage}
// @Security BearerAuth
// @Router /api/v1/support/messages [get]
func SupportGetMessages(c *gin.Context) {
	me := middleware.CurrentUser(c)
	// 客服必须指定 user_id
	if security.IsSupportStaff(me) && c.Query("user_id") == "" {
		Fail(c, http.StatusBadRequest, 400, "需要指定 user_id")
		return
	}
	svc := services.NewSupportService(database.DB)
	msgs, err := svc.GetMessages(me, c.Query("user_id"), c.Query("since_id"))
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(msgs))
	for i := range msgs {
		out = append(out, svc.MsgOut(&msgs[i]))
	}
	OK(c, out)
}

// SupportMarkRead godoc
// @Summary 标记消息已读
// @Tags 客服
// @Produce json
// @Param user_id query int false "用户ID（客服用）"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/support/messages/read [post]
func SupportMarkRead(c *gin.Context) {
	me := middleware.CurrentUser(c)
	if security.IsSupportStaff(me) && c.Query("user_id") == "" {
		Fail(c, http.StatusBadRequest, 400, "需要 user_id")
		return
	}
	svc := services.NewSupportService(database.DB)
	svc.MarkRead(me, c.Query("user_id"))
	OK(c, gin.H{"ok": true})
}

// SupportUnread godoc
// @Summary 获取未读消息数量
// @Tags 客服
// @Produce json
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/support/unread [get]
func SupportUnread(c *gin.Context) {
	me := middleware.CurrentUser(c)
	svc := services.NewSupportService(database.DB)
	count, _ := svc.UnreadCount(me)
	OK(c, gin.H{"count": count})
}

// SupportConversations godoc
// @Summary 获取所有会话列表（客服）
// @Tags 客服
// @Produce json
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/support/conversations [get]
func SupportConversations(c *gin.Context) {
	me := middleware.CurrentUser(c)
	if !security.IsSupportStaff(me) {
		Fail(c, http.StatusForbidden, 403, "无客服权限")
		return
	}
	svc := services.NewSupportService(database.DB)
	convs, err := svc.ListConversations()
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	OK(c, convs)
}

// previewText 生成消息预览文本（最多 40 字），无文本时使用附件名。
func previewText(content, attachmentName string) string {
	preview := strings.TrimSpace(content)
	if len(preview) > 40 {
		preview = preview[:40]
	}
	if preview == "" {
		if attachmentName != "" {
			return "[" + attachmentName + "]"
		}
		return "[附件]"
	}
	return preview
}
