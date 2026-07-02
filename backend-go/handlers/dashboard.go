package handlers

import (
	"net/http"
	"time"

	"simnexus-go/database"
	"simnexus-go/models"

	"github.com/gin-gonic/gin"
)

// DashboardStats godoc
// @Summary 获取仪表盘统计数据
// @Tags 仪表盘
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/dashboard/stats [get]
func DashboardStats(c *gin.Context) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	sevenDaysAgo := today.AddDate(0, 0, -6)

	type row struct {
		Day    string
		Status string
		Cnt    int64
	}
	var smsRows []row
	database.DB.Model(&models.SmsMessage{}).
		Select("date(created_at) as day, status, count(*) as cnt").
		Where("direction = ? AND date(created_at) >= ?", models.SmsOutbound, sevenDaysAgo.Format("2006-01-02")).
		Group("day, status").Scan(&smsRows)

	trend := map[string]map[string]interface{}{}
	order := []string{}
	for i := 0; i < 7; i++ {
		d := today.AddDate(0, 0, -6+i).Format("2006-01-02")
		trend[d] = map[string]interface{}{"date": d, "sent": 0, "failed": 0}
		order = append(order, d)
	}
	for _, r := range smsRows {
		if t, ok := trend[r.Day]; ok {
			switch r.Status {
			case models.SmsSent:
				t["sent"] = r.Cnt
			case models.SmsFailed:
				t["failed"] = r.Cnt
			}
		}
	}
	trendList := make([]map[string]interface{}, 0, 7)
	for _, d := range order {
		trendList = append(trendList, trend[d])
	}

	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	var monthRows []row
	database.DB.Model(&models.SmsMessage{}).
		Select("status, count(*) as cnt").
		Where("direction = ? AND date(created_at) >= ?", models.SmsOutbound, monthStart).
		Group("status").Scan(&monthRows)
	monthStats := gin.H{"sent": int64(0), "failed": int64(0), "pending": int64(0)}
	for _, r := range monthRows {
		switch r.Status {
		case models.SmsSent:
			monthStats["sent"] = r.Cnt
		case models.SmsFailed:
			monthStats["failed"] = r.Cnt
		default:
			monthStats["pending"] = monthStats["pending"].(int64) + r.Cnt
		}
	}

	var taskRows []row
	database.DB.Model(&models.SmsScheduledTask{}).
		Select("status, count(*) as cnt").Group("status").Scan(&taskRows)
	taskStats := gin.H{"active": int64(0), "paused": int64(0), "completed": int64(0), "failed": int64(0)}
	for _, r := range taskRows {
		if _, ok := taskStats[r.Status]; ok {
			taskStats[r.Status] = r.Cnt
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"sms_trend": trendList,
		"month_sms": monthStats,
		"tasks":     taskStats,
	})
}
