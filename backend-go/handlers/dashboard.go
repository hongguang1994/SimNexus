package handlers

import (
	"simnexus-go/database"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// DashboardStats godoc
// @Summary 获取仪表盘统计数据
// @Tags 仪表盘
// @Produce json
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/dashboard/stats [get]
func DashboardStats(c *gin.Context) {
	svc := services.NewDashboardService(database.DB)
	stats := svc.GetStats()
	OK(c, gin.H{
		"sms_trend": stats.SmsTrend,
		"month_sms": stats.MonthSms,
		"tasks":     stats.Tasks,
	})
}
