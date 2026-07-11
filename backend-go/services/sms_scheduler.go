package services

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"simnexus-go/database"
	"simnexus-go/models"

	"github.com/robfig/cron/v3"
)

var (
	cronRunner *cron.Cron                // cron 调度器，使用 UTC 时区
	jobIDs     = map[uint]cron.EntryID{} // task.ID → cron entry ID，用于取消定期任务
	onceTimers = map[uint]*time.Timer{}  // task.ID → time.Timer，用于取消一次性任务
	schedMu    sync.Mutex                // 保护 jobIDs 和 onceTimers 的并发访问
)

// StartScheduler launches the cron runner and the 60s reload loop.
func StartScheduler() {
	cronRunner = cron.New(cron.WithLocation(time.UTC))
	cronRunner.Start()
	ReloadTasks()
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		for range ticker.C {
			ReloadTasks()
		}
	}()
	slog.Info("SMS scheduler started")
}

// ReloadTasks syncs cron/one-shot jobs with active DB tasks.
func ReloadTasks() {
	schedMu.Lock()
	defer schedMu.Unlock()

	var tasks []models.SmsScheduledTask
	database.DB.Where("status = ?", models.TaskActive).Find(&tasks)
	active := map[uint]bool{}
	now := time.Now().UTC()

	for _, task := range tasks {
		active[task.ID] = true
		if _, ok := jobIDs[task.ID]; ok {
			continue
		}
		if _, ok := onceTimers[task.ID]; ok {
			continue
		}
		// skip past-due one-shot tasks
		if task.SendOnceAt != nil && !task.SendOnceAt.After(now) {
			continue
		}
		scheduleLocked(task)
	}

	// remove jobs for deleted/paused tasks
	for id, entry := range jobIDs {
		if !active[id] {
			cronRunner.Remove(entry)
			delete(jobIDs, id)
		}
	}
	for id, t := range onceTimers {
		if !active[id] {
			t.Stop()
			delete(onceTimers, id)
		}
	}
}

// ScheduleTask registers a single task (used right after creation).
func ScheduleTask(task models.SmsScheduledTask) {
	schedMu.Lock()
	defer schedMu.Unlock()
	scheduleLocked(task)
}

// scheduleLocked 在持有 schedMu 锁的情况下注册单个任务，需调用方加锁。
// cron 任务注册到 cronRunner，一次性任务使用 time.AfterFunc。
func scheduleLocked(task models.SmsScheduledTask) {
	id := task.ID
	if task.CronExpression != nil && strings.TrimSpace(*task.CronExpression) != "" {
		entry, err := cronRunner.AddFunc(strings.TrimSpace(*task.CronExpression), func() {
			ExecuteTask(id)
		})
		if err != nil {
			slog.Error("invalid cron expression", "task_id", id, "err", err)
			return
		}
		jobIDs[id] = entry
		if next := cronRunner.Entry(entry).Next; !next.IsZero() {
			nx := next.UTC()
			database.DB.Model(&models.SmsScheduledTask{}).Where("id = ?", id).Update("next_run_at", nx)
		}
		slog.Info("scheduled cron task", "task_id", id, "name", task.Name)
	} else if task.SendOnceAt != nil {
		delay := time.Until(*task.SendOnceAt)
		if delay < 0 {
			delay = 0
		}
		onceTimers[id] = time.AfterFunc(delay, func() {
			ExecuteTask(id)
			schedMu.Lock()
			delete(onceTimers, id)
			schedMu.Unlock()
		})
		database.DB.Model(&models.SmsScheduledTask{}).Where("id = ?", id).Update("next_run_at", task.SendOnceAt.UTC())
		slog.Info("scheduled one-shot task", "task_id", id, "name", task.Name, "fire_at", task.SendOnceAt)
	}
}

// RemoveTask cancels any scheduled job for the task.
func RemoveTask(taskID uint) {
	schedMu.Lock()
	defer schedMu.Unlock()
	if entry, ok := jobIDs[taskID]; ok {
		cronRunner.Remove(entry)
		delete(jobIDs, taskID)
	}
	if t, ok := onceTimers[taskID]; ok {
		t.Stop()
		delete(onceTimers, taskID)
	}
}

// ExecuteTask fires a task: sends to all recipients and updates status.
func ExecuteTask(taskID uint) {
	db := database.DB
	var task models.SmsScheduledTask
	if db.First(&task, taskID).Error != nil || task.Status != models.TaskActive {
		return
	}
	var modem models.Modem
	if db.First(&modem, task.ModemID).Error != nil || modem.MmObjectPath == "" {
		slog.Warn("task modem not found", "task_id", taskID)
		return
	}

	obj := modem.MmObjectPath
	isZte := strings.HasPrefix(obj, "zte:")
	mmIndex := ""
	if !isZte {
		m := reModemIdx.FindStringSubmatch(obj)
		if m == nil {
			return
		}
		mmIndex = m[1]
	}

	channel := models.SmsChannelCellular
	if modem.VowifiMode {
		channel = models.SmsChannelVowifi
	}
	recipients := []string(task.Recipients)
	failCount := 0
	for _, phone := range recipients {
		var success bool
		var message string
		if modem.VowifiMode {
			// VoWiFi 模式的卡已被独占，走常驻 VoWiFi 会话发送
			success, message = GetVowifiManager().SendSMS(&modem, phone, task.Content)
		} else if isZte {
			success = ZteSendSMS(phone, task.Content)
			if !success {
				message = "ZTE send failed"
			}
		} else {
			success, message = SendSMS(mmIndex, phone, task.Content)
		}
		now := time.Now()
		sms := models.SmsMessage{
			ModemID:         modem.ID,
			Direction:       models.SmsOutbound,
			Channel:         channel,
			PhoneNumber:     phone,
			Content:         task.Content,
			Status:          models.SmsSent,
			ScheduledTaskID: &task.ID,
			CreatedByID:     task.CreatedByID,
		}
		if success {
			sms.SentAt = &now
		} else {
			sms.Status = models.SmsFailed
			em := message
			sms.ErrorMessage = &em
			failCount++
		}
		db.Create(&sms)
		broadcastMessage(&sms) // WebSocket 实时推送
	}

	if failCount > 0 {
		label := modem.Alias
		if label == "" {
			label = modem.Model
		}
		if label == "" {
			label = fmt.Sprintf("设备#%d", modem.ID)
		}
		body := fmt.Sprintf("任务「%s」[%s] 有 %d/%d 条短信发送失败", task.Name, label, failCount, len(recipients))
		if task.CreatedByID != nil {
			var creator models.User
			if db.First(&creator, *task.CreatedByID).Error == nil && creator.IsAdmin() {
				Push("task_failed", "定时任务失败", body, "admin", nil)
			} else {
				Push("task_failed", "定时任务失败", body, "user", task.CreatedByID)
			}
		} else {
			Push("task_failed", "定时任务失败", body, "admin", nil)
		}
	}

	now := time.Now()
	task.LastRunAt = &now
	task.RunCount++
	if task.SendOnceAt != nil {
		if failCount == len(recipients) {
			task.Status = models.TaskFailed
		} else {
			task.Status = models.TaskCompleted
		}
	}
	db.Save(&task)
	slog.Info("task executed", "task_id", taskID, "recipients", len(recipients), "failed", failCount)
}
