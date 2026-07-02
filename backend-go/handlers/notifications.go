package handlers

import (
	"net/http"
	"strconv"

	"simnexus-go/database"
	"simnexus-go/middleware"
	"simnexus-go/models"
	"simnexus-go/security"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// visibleNotificationFilter 对通知查询追加按 audience 过滤的 WHERE 条件，
// 规则：all 对所有人可见；user 仅对 target_user_id 可见；admin/support 按角色可见。
func visibleNotificationFilter(me *models.User, q *gorm.DB) *gorm.DB {
	// base: 'all' OR ('user' AND target=me)
	cond := "audience = 'all' OR (audience = 'user' AND target_user_id = ?)"
	args := []interface{}{me.ID}
	if me.IsAdmin() {
		cond += " OR audience = 'admin' OR audience = 'support'"
	} else if security.IsSupportStaff(me) {
		cond += " OR audience = 'support'"
	}
	return q.Where(cond, args...)
}

// ListNotifications godoc
// @Summary 获取通知列表
// @Tags 通知
// @Produce json
// @Param limit query int false "每页数量"
// @Success 200 {array} models.Notification
// @Security BearerAuth
// @Router /api/v1/notifications [get]
func ListNotifications(c *gin.Context) {
	me := middleware.CurrentUser(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit > 100 {
		limit = 100
	}
	var ns []models.Notification
	visibleNotificationFilter(me, database.DB.Model(&models.Notification{})).
		Order("id desc").Limit(limit).Find(&ns)
	c.JSON(http.StatusOK, ns)
}

// UnreadCount godoc
// @Summary 获取未读通知数量
// @Tags 通知
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/notifications/unread-count [get]
func UnreadCount(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var count int64
	visibleNotificationFilter(me, database.DB.Model(&models.Notification{})).
		Where("is_read = ?", false).Count(&count)
	c.JSON(http.StatusOK, gin.H{"count": count})
}

// MarkAllRead godoc
// @Summary 一键标记所有通知为已读
// @Tags 通知
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/notifications/read-all [post]
func MarkAllRead(c *gin.Context) {
	me := middleware.CurrentUser(c)
	visibleNotificationFilter(me, database.DB.Model(&models.Notification{})).
		Where("is_read = ?", false).Update("is_read", true)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// MarkOneRead godoc
// @Summary 标记单条通知为已读
// @Tags 通知
// @Produce json
// @Param id path int true "通知ID"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/notifications/{id}/read [post]
func MarkOneRead(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var n models.Notification
	if visibleNotificationFilter(me, database.DB.Model(&models.Notification{})).
		Where("id = ?", id).First(&n).Error == nil {
		n.IsRead = true
		database.DB.Save(&n)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
