package services

import (
	"time"

	"simnexus-go/models"

	"gorm.io/gorm"
)

// DashboardService 提供仪表盘统计数据的数据库聚合查询。
type DashboardService struct {
	db *gorm.DB
}

// NewDashboardService 创建 DashboardService 实例。
func NewDashboardService(db *gorm.DB) *DashboardService {
	return &DashboardService{db: db}
}

// DashboardStats 仪表盘统计数据，包含近 7 天趋势、本月汇总和任务状态分布。
type DashboardStats struct {
	SmsTrend []map[string]interface{} `json:"sms_trend"` // 近 7 天每日发送/失败条数
	MonthSms map[string]interface{}   `json:"month_sms"` // 本月发送/失败/待发统计
	Tasks    map[string]interface{}   `json:"tasks"`     // 各状态任务数量
}

// smsRow 用于接收 GROUP BY 聚合查询结果。
type smsRow struct {
	Day       string // 日期字符串 yyyy-MM-dd
	Direction string // inbound | outbound
	Status    string // 短信状态
	Cnt       int64  // 条数
}

// GetStats 计算并返回仪表盘全部统计数据。
func (s *DashboardService) GetStats() DashboardStats {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	sevenDaysAgo := today.AddDate(0, 0, -6) // 包含今天共 7 天

	// ---- 近 7 天每日趋势（发送成功/失败 + 接收）----
	var smsRows []smsRow
	s.db.Model(&models.SmsMessage{}).
		Select("date(created_at) as day, direction, status, count(*) as cnt").
		Where("date(created_at) >= ?", sevenDaysAgo.Format("2006-01-02")).
		Group("day, direction, status").
		Scan(&smsRows)

	// 初始化 7 天日期框架，确保没有数据的日期也有占位
	trend := make(map[string]map[string]interface{}, 7)
	order := make([]string, 0, 7)
	for i := 0; i < 7; i++ {
		d := today.AddDate(0, 0, -6+i).Format("2006-01-02")
		trend[d] = map[string]interface{}{"date": d, "sent": int64(0), "failed": int64(0), "received": int64(0)}
		order = append(order, d)
	}
	for _, r := range smsRows {
		t, ok := trend[r.Day]
		if !ok {
			continue
		}
		if r.Direction == models.SmsInbound {
			t["received"] = t["received"].(int64) + r.Cnt // 收件不分状态，全部计入接收
			continue
		}
		switch r.Status {
		case models.SmsSent:
			t["sent"] = r.Cnt
		case models.SmsFailed:
			t["failed"] = r.Cnt
		}
	}
	trendList := make([]map[string]interface{}, 0, 7)
	for _, d := range order {
		trendList = append(trendList, trend[d])
	}

	// ---- 本月短信汇总 ----
	// monthStart 取本月 1 日 00:00:00 UTC
	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	var monthRows []smsRow
	s.db.Model(&models.SmsMessage{}).
		Select("status, count(*) as cnt").
		Where("direction = ? AND date(created_at) >= ?", models.SmsOutbound, monthStart).
		Group("status").
		Scan(&monthRows)
	monthStats := map[string]interface{}{"sent": int64(0), "failed": int64(0), "pending": int64(0), "received": int64(0)}
	for _, r := range monthRows {
		switch r.Status {
		case models.SmsSent:
			monthStats["sent"] = r.Cnt
		case models.SmsFailed:
			monthStats["failed"] = r.Cnt
		default:
			// 其余状态（pending / sending 等）统一归入 pending
			monthStats["pending"] = monthStats["pending"].(int64) + r.Cnt
		}
	}
	// 本月接收（收件）总数
	var recvMonth int64
	s.db.Model(&models.SmsMessage{}).
		Where("direction = ? AND date(created_at) >= ?", models.SmsInbound, monthStart).
		Count(&recvMonth)
	monthStats["received"] = recvMonth

	// ---- 定时任务状态分布 ----
	var taskRows []smsRow
	s.db.Model(&models.SmsScheduledTask{}).
		Select("status, count(*) as cnt").
		Group("status").
		Scan(&taskRows)
	taskStats := map[string]interface{}{
		"active": int64(0), "paused": int64(0), "completed": int64(0), "failed": int64(0),
	}
	for _, r := range taskRows {
		if _, ok := taskStats[r.Status]; ok {
			taskStats[r.Status] = r.Cnt
		}
	}

	return DashboardStats{
		SmsTrend: trendList,
		MonthSms: monthStats,
		Tasks:    taskStats,
	}
}
