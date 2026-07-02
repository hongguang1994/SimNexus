package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"simnexus-go/middleware"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// LogsSSE godoc
// @Summary 实时日志SSE流（仅管理员）
// @Tags 系统
// @Produce text/event-stream
// @Success 200 {string} string "SSE流"
// @Security BearerAuth
// @Router /api/v1/admin/logs/stream [get]
func LogsSSE(c *gin.Context) {
	user := middleware.CurrentUser(c)
	if user == nil || !user.IsAdmin() {
		Fail(c, http.StatusForbidden, 403, "需要管理员权限")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// 先推送历史缓存
	for _, entry := range services.GlobalLog.Snapshot() {
		data, _ := json.Marshal(entry)
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	}
	c.Writer.Flush()

	// 订阅新日志
	ch := services.GlobalLog.Subscribe(c.Request.Context())
	for entry := range ch {
		data, _ := json.Marshal(entry)
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		c.Writer.Flush()
	}
}
