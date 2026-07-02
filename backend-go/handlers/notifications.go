package handlers

import (
	"net/http"
	"strconv"

	"simnexus-go/database"
	"simnexus-go/middleware"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// ListNotifications godoc
// @Summary 获取通知列表
// @Tags 通知
// @Produce json
// @Param limit query int false "每页数量（最大100）"
// @Success 200 {object} handlers.R{data=[]models.Notification}
// @Security BearerAuth
// @Router /api/v1/notifications [get]
func ListNotifications(c *gin.Context) {
	me := middleware.CurrentUser(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	svc := services.NewNotificationService(database.DB)
	ns, err := svc.ListNotifications(me, limit)
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	OK(c, ns)
}

// UnreadCount godoc
// @Summary 获取未读通知数量
// @Tags 通知
// @Produce json
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/notifications/unread-count [get]
func UnreadCount(c *gin.Context) {
	me := middleware.CurrentUser(c)
	svc := services.NewNotificationService(database.DB)
	count, err := svc.UnreadCount(me)
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	OK(c, gin.H{"count": count})
}

// MarkAllRead godoc
// @Summary 一键标记所有通知为已读
// @Tags 通知
// @Produce json
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/notifications/read-all [post]
func MarkAllRead(c *gin.Context) {
	me := middleware.CurrentUser(c)
	svc := services.NewNotificationService(database.DB)
	svc.MarkAllRead(me)
	OK(c, gin.H{"ok": true})
}

// MarkOneRead godoc
// @Summary 标记单条通知为已读
// @Tags 通知
// @Produce json
// @Param id path int true "通知ID"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/notifications/{id}/read [post]
func MarkOneRead(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	svc := services.NewNotificationService(database.DB)
	svc.MarkOneRead(me, uint(id))
	OK(c, gin.H{"ok": true})
}
