package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"simnexus-go/database"
	"simnexus-go/middleware"
	"simnexus-go/models"
	"simnexus-go/security"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// userVisibleModemIDs 返回当前用户可查看的设备 ID 列表（不限权限级别）。
// unrestricted=true 表示管理员，可见所有设备。
func userVisibleModemIDs(u *models.User) ([]uint, bool) {
	if u.IsAdmin() {
		return nil, true
	}
	return security.GetUserModemGrants(database.DB, u.ID, "", u), false
}

// requireUseGrant 检查用户是否对指定设备拥有 use 级别权限（可发送短信）。
func requireUseGrant(u *models.User, modemID uint) bool {
	if u.IsAdmin() {
		return true
	}
	useIDs := security.GetUserModemGrants(database.DB, u.ID, models.LevelUse, u)
	return security.ContainsUint(useIDs, modemID)
}

type smsSendRequest struct {
	ModemID     uint   `json:"modem_id"`
	PhoneNumber string `json:"phone_number"`
	Content     string `json:"content"`
}

// SendSMS godoc
// @Summary 立即发送短信
// @Tags 短信
// @Accept json
// @Produce json
// @Param body body smsSendRequest true "发送参数"
// @Success 200 {object} models.SmsMessage
// @Security BearerAuth
// @Router /api/v1/sms/send [post]
func SendSMS(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var req smsSendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, 400, "请求格式错误")
		return
	}
	var modem models.Modem
	if database.DB.First(&modem, req.ModemID).Error != nil {
		Fail(c, http.StatusNotFound, 404, "Modem not found")
		return
	}
	if !requireUseGrant(me, modem.ID) {
		Fail(c, http.StatusForbidden, 403, "无该SIM卡的使用权限，请先申请")
		return
	}

	obj := modem.MmObjectPath
	var success bool
	var message string
	if strings.HasPrefix(obj, "zte:") {
		success = services.ZteSendSMS(req.PhoneNumber, req.Content)
		if !success {
			message = "ZTE device returned failure"
		}
	} else {
		m := reModem.FindStringSubmatch(obj)
		if m == nil {
			Fail(c, http.StatusServiceUnavailable, 503, "Modem not available")
			return
		}
		success, message = services.SendSMS(m[1], req.PhoneNumber, req.Content)
	}

	now := time.Now()
	sms := models.SmsMessage{
		ModemID:     modem.ID,
		Direction:   models.SmsOutbound,
		PhoneNumber: req.PhoneNumber,
		Content:     req.Content,
		Status:      models.SmsSent,
		CreatedByID: &me.ID,
	}
	if success {
		sms.SentAt = &now
	} else {
		sms.Status = models.SmsFailed
		sms.ErrorMessage = &message
	}
	database.DB.Create(&sms)

	if !success {
		label := modemDisplayLabel(&modem)
		body := "发往 " + req.PhoneNumber + " 的短信发送失败：" + message
		if me.IsAdmin() {
			services.Push("sms_failed", "短信发送失败", "["+label+"] "+body, "admin", nil)
		} else {
			services.Push("sms_failed", "短信发送失败", "["+label+"] "+body, "user", &me.ID)
		}
		Fail(c, http.StatusBadGateway, 502, "SMS send failed: " + message)
		return
	}
	OK(c, sms)
}

// modemDisplayLabel 返回设备展示名称，优先级：别名 > 型号 > 设备#ID。
func modemDisplayLabel(m *models.Modem) string {
	if m.Alias != "" {
		return m.Alias
	}
	if m.Model != "" {
		return m.Model
	}
	return "设备#" + strconv.Itoa(int(m.ID))
}

// ListMessages godoc
// @Summary 获取短信记录
// @Tags 短信
// @Produce json
// @Param modem_id query int false "设备ID过滤"
// @Param direction query string false "方向过滤(inbound/outbound)"
// @Param skip query int false "偏移量"
// @Param limit query int false "每页数量"
// @Success 200 {array} models.SmsMessage
// @Security BearerAuth
// @Router /api/v1/sms/messages [get]
func ListMessages(c *gin.Context) {
	me := middleware.CurrentUser(c)
	q := database.DB.Model(&models.SmsMessage{})
	ids, unrestricted := userVisibleModemIDs(me)
	if !unrestricted {
		if len(ids) == 0 {
			OK(c, []models.SmsMessage{})
			return
		}
		q = q.Where("modem_id IN ?", ids)
	}
	if v := c.Query("modem_id"); v != "" {
		q = q.Where("modem_id = ?", v)
	}
	if v := c.Query("direction"); v != "" {
		q = q.Where("direction = ?", v)
	}
	skip, _ := strconv.Atoi(c.DefaultQuery("skip", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	var msgs []models.SmsMessage
	q.Order("created_at desc").Offset(skip).Limit(limit).Find(&msgs)
	OK(c, msgs)
}

// deleteFromModem 将收件短信从物理设备上删除（避免设备存储满），仅处理收件方向。
func deleteFromModem(msg *models.SmsMessage) {
	if msg.Direction != models.SmsInbound || msg.MmSmsIndex == "" {
		return
	}
	var modem models.Modem
	if database.DB.First(&modem, msg.ModemID).Error == nil {
		services.DeleteSmsFromModem(modem.MmObjectPath, msg.MmSmsIndex)
	}
}

// DeleteMessage godoc
// @Summary 删除单条短信记录
// @Tags 短信
// @Produce json
// @Param id path int true "记录ID"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sms/messages/{id} [delete]
func DeleteMessage(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var msg models.SmsMessage
	if database.DB.First(&msg, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "记录不存在")
		return
	}
	ids, unrestricted := userVisibleModemIDs(me)
	if !unrestricted && !security.ContainsUint(ids, msg.ModemID) {
		Fail(c, http.StatusForbidden, 403, "无权限")
		return
	}
	deleteFromModem(&msg)
	database.DB.Delete(&msg)
	OK(c, gin.H{"ok": true})
}

type batchDeleteBody struct {
	IDs []uint `json:"ids"`
}

// BatchDeleteMessages godoc
// @Summary 批量删除短信记录
// @Tags 短信
// @Accept json
// @Produce json
// @Param body body batchDeleteBody true "ID列表"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sms/messages/batch-delete [post]
func BatchDeleteMessages(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var body batchDeleteBody
	c.ShouldBindJSON(&body)
	if len(body.IDs) == 0 {
		OK(c, gin.H{"deleted": 0})
		return
	}
	q := database.DB.Where("id IN ?", body.IDs)
	ids, unrestricted := userVisibleModemIDs(me)
	if !unrestricted {
		q = q.Where("modem_id IN ?", ids)
	}
	var msgs []models.SmsMessage
	q.Find(&msgs)
	for i := range msgs {
		deleteFromModem(&msgs[i])
		database.DB.Delete(&msgs[i])
	}
	OK(c, gin.H{"deleted": len(msgs)})
}

// Templates

// ListTemplates godoc
// @Summary 获取短信模板列表
// @Tags 短信
// @Produce json
// @Success 200 {array} models.SmsTemplate
// @Security BearerAuth
// @Router /api/v1/sms/templates [get]
func ListTemplates(c *gin.Context) {
	var tpls []models.SmsTemplate
	database.DB.Find(&tpls)
	OK(c, tpls)
}

// CreateTemplate godoc
// @Summary 创建短信模板
// @Tags 短信
// @Accept json
// @Produce json
// @Param body body models.SmsTemplate true "模板内容"
// @Success 200 {object} models.SmsTemplate
// @Security BearerAuth
// @Router /api/v1/sms/templates [post]
func CreateTemplate(c *gin.Context) {
	var tpl models.SmsTemplate
	if err := c.ShouldBindJSON(&tpl); err != nil {
		Fail(c, http.StatusBadRequest, 400, "请求格式错误")
		return
	}
	database.DB.Create(&tpl)
	OK(c, tpl)
}

// DeleteTemplate godoc
// @Summary 删除短信模板
// @Tags 短信
// @Produce json
// @Param id path int true "模板ID"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sms/templates/{id} [delete]
func DeleteTemplate(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var tpl models.SmsTemplate
	if database.DB.First(&tpl, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "Template not found")
		return
	}
	database.DB.Delete(&tpl)
	OK(c, gin.H{"ok": true})
}

// Scheduled tasks

// taskToOut 将任务记录转为 API 响应，附加创建者用户名。
func taskToOut(t *models.SmsScheduledTask) gin.H {
	out := gin.H{}
	remarshal(t, &out)
	if t.CreatedByID != nil {
		var u models.User
		if database.DB.First(&u, *t.CreatedByID).Error == nil {
			out["created_by_username"] = u.Username
		}
	}
	return out
}

// ListTasks godoc
// @Summary 获取定时任务列表
// @Tags 短信
// @Produce json
// @Success 200 {array} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sms/tasks [get]
func ListTasks(c *gin.Context) {
	me := middleware.CurrentUser(c)
	q := database.DB.Model(&models.SmsScheduledTask{})
	if !me.IsAdmin() {
		ids, unrestricted := userVisibleModemIDs(me)
		if !unrestricted {
			q = q.Where("modem_id IN ?", ids)
		}
		q = q.Where("created_by_id = ?", me.ID)
	}
	var tasks []models.SmsScheduledTask
	q.Order("id desc").Find(&tasks)
	out := make([]gin.H, 0, len(tasks))
	for i := range tasks {
		out = append(out, taskToOut(&tasks[i]))
	}
	OK(c, out)
}

type taskCreate struct {
	Name           string     `json:"name"`
	ModemID        uint       `json:"modem_id"`
	Recipients     []string   `json:"recipients"`
	Content        string     `json:"content"`
	CronExpression *string    `json:"cron_expression"`
	SendOnceAt     *time.Time `json:"send_once_at"`
}

// CreateTask godoc
// @Summary 创建定时任务
// @Tags 短信
// @Accept json
// @Produce json
// @Param body body taskCreate true "任务参数"
// @Success 200 {object} models.SmsScheduledTask
// @Security BearerAuth
// @Router /api/v1/sms/tasks [post]
func CreateTask(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var data taskCreate
	if err := c.ShouldBindJSON(&data); err != nil {
		Fail(c, http.StatusBadRequest, 400, "请求格式错误")
		return
	}
	if !requireUseGrant(me, data.ModemID) {
		Fail(c, http.StatusForbidden, 403, "无该SIM卡的使用权限，请先申请")
		return
	}
	var cron *string
	if data.CronExpression != nil {
		t := strings.TrimSpace(*data.CronExpression)
		if t != "" {
			cron = &t
		}
	}
	if cron == nil && data.SendOnceAt == nil {
		Fail(c, http.StatusBadRequest, 400, "Provide cron_expression or send_once_at")
		return
	}
	task := models.SmsScheduledTask{
		Name:           data.Name,
		ModemID:        data.ModemID,
		Recipients:     models.JSONList(data.Recipients),
		Content:        data.Content,
		CronExpression: cron,
		SendOnceAt:     data.SendOnceAt,
		Status:         models.TaskActive,
		CreatedByID:    &me.ID,
	}
	database.DB.Create(&task)
	services.ScheduleTask(task)
	OK(c, task)
}

type taskUpdate struct {
	Name           *string    `json:"name"`
	Recipients     *[]string  `json:"recipients"`
	Content        *string    `json:"content"`
	CronExpression *string    `json:"cron_expression"`
	SendOnceAt     *time.Time `json:"send_once_at"`
	Status         *string    `json:"status"`
}

// UpdateTask godoc
// @Summary 修改定时任务
// @Tags 短信
// @Accept json
// @Produce json
// @Param id path int true "任务ID"
// @Param body body taskUpdate true "修改字段"
// @Success 200 {object} models.SmsScheduledTask
// @Security BearerAuth
// @Router /api/v1/sms/tasks/{id} [patch]
func UpdateTask(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var task models.SmsScheduledTask
	if database.DB.First(&task, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "Task not found")
		return
	}
	if !me.IsAdmin() && (task.CreatedByID == nil || *task.CreatedByID != me.ID) {
		Fail(c, http.StatusForbidden, 403, "无权修改此任务")
		return
	}
	var data taskUpdate
	c.ShouldBindJSON(&data)
	if data.Name != nil {
		task.Name = *data.Name
	}
	if data.Recipients != nil {
		task.Recipients = models.JSONList(*data.Recipients)
	}
	if data.Content != nil {
		task.Content = *data.Content
	}
	if data.CronExpression != nil {
		task.CronExpression = data.CronExpression
	}
	if data.SendOnceAt != nil {
		task.SendOnceAt = data.SendOnceAt
	}
	if data.Status != nil {
		task.Status = *data.Status
	}
	database.DB.Save(&task)
	services.RemoveTask(task.ID)
	if task.Status == models.TaskActive {
		services.ScheduleTask(task)
	}
	OK(c, task)
}

// DeleteTask godoc
// @Summary 删除定时任务
// @Tags 短信
// @Produce json
// @Param id path int true "任务ID"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sms/tasks/{id} [delete]
func DeleteTask(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var task models.SmsScheduledTask
	if database.DB.First(&task, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "Task not found")
		return
	}
	if !me.IsAdmin() && (task.CreatedByID == nil || *task.CreatedByID != me.ID) {
		Fail(c, http.StatusForbidden, 403, "无权删除此任务")
		return
	}
	services.RemoveTask(task.ID)
	database.DB.Delete(&task)
	OK(c, gin.H{"ok": true})
}

// RunTaskNow godoc
// @Summary 立即执行定时任务
// @Tags 短信
// @Produce json
// @Param id path int true "任务ID"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sms/tasks/{id}/run-now [post]
func RunTaskNow(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var task models.SmsScheduledTask
	if database.DB.First(&task, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "Task not found")
		return
	}
	services.ExecuteTask(uint(id))
	OK(c, gin.H{"ok": true})
}

// AdminListTasks godoc
// @Summary 管理员查看所有任务
// @Tags 短信
// @Produce json
// @Param user_id query int false "按用户过滤"
// @Param status query string false "按状态过滤"
// @Success 200 {array} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sms/admin/tasks [get]
func AdminListTasks(c *gin.Context) {
	me := middleware.CurrentUser(c)
	q := database.DB.Model(&models.SmsScheduledTask{})
	if !me.IsAdmin() {
		q = q.Where("created_by_id = ?", me.ID)
	} else if uid := c.Query("user_id"); uid != "" {
		q = q.Where("created_by_id = ?", uid)
	}
	if st := c.Query("status"); st != "" {
		q = q.Where("status = ?", st)
	}
	var tasks []models.SmsScheduledTask
	q.Order("id desc").Find(&tasks)
	out := make([]gin.H, 0, len(tasks))
	for i := range tasks {
		out = append(out, taskToOut(&tasks[i]))
	}
	OK(c, out)
}

// AdminTaskStats godoc
// @Summary 获取任务统计数据
// @Tags 短信
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sms/admin/tasks/stats [get]
func AdminTaskStats(c *gin.Context) {
	me := middleware.CurrentUser(c)
	q := database.DB.Model(&models.SmsScheduledTask{})
	if !me.IsAdmin() {
		q = q.Where("created_by_id = ?", me.ID)
	}
	var tasks []models.SmsScheduledTask
	q.Find(&tasks)
	stats := gin.H{"total": len(tasks), "active": 0, "paused": 0, "completed": 0, "failed": 0}
	for _, t := range tasks {
		switch t.Status {
		case models.TaskActive:
			stats["active"] = stats["active"].(int) + 1
		case models.TaskPaused:
			stats["paused"] = stats["paused"].(int) + 1
		case models.TaskCompleted:
			stats["completed"] = stats["completed"].(int) + 1
		case models.TaskFailed:
			stats["failed"] = stats["failed"].(int) + 1
		}
	}
	OK(c, stats)
}

// AdminTaskHistory godoc
// @Summary 获取任务执行历史
// @Tags 短信
// @Produce json
// @Param id path int true "任务ID"
// @Param limit query int false "返回条数"
// @Success 200 {array} models.SmsMessage
// @Security BearerAuth
// @Router /api/v1/sms/admin/tasks/{id}/history [get]
func AdminTaskHistory(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var task models.SmsScheduledTask
	if database.DB.First(&task, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "任务不存在")
		return
	}
	if !me.IsAdmin() && (task.CreatedByID == nil || *task.CreatedByID != me.ID) {
		Fail(c, http.StatusForbidden, 403, "无权查看该任务")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	var msgs []models.SmsMessage
	database.DB.Where("scheduled_task_id = ?", id).Order("created_at desc").Limit(limit).Find(&msgs)
	OK(c, msgs)
}
