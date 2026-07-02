package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"simnexus-go/database"
	"simnexus-go/middleware"
	"simnexus-go/models"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// smsSendRequest 立即发送短信的请求体。
type smsSendRequest struct {
	ModemID     uint   `json:"modem_id"     binding:"required,gt=0"`
	PhoneNumber string `json:"phone_number" binding:"required"`
	Content     string `json:"content"      binding:"required,max=1000"`
}

// SendSMS godoc
// @Summary 立即发送短信
// @Tags 短信
// @Accept json
// @Produce json
// @Param body body smsSendRequest true "发送参数"
// @Success 200 {object} handlers.R{data=models.SmsMessage}
// @Security BearerAuth
// @Router /api/v1/sms/send [post]
func SendSMS(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var req smsSendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, 400, "请求格式错误")
		return
	}
	svc := services.NewSmsService(database.DB)
	result, err := svc.SendSMS(me, req.ModemID, req.PhoneNumber, req.Content)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrNoUsePerm):
			Fail(c, http.StatusForbidden, 403, err.Error())
		case errors.Is(err, services.ErrModemNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		case errors.Is(err, services.ErrModemUnavailable):
			Fail(c, http.StatusServiceUnavailable, 503, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, err.Error())
		}
		return
	}
	if !result.Success {
		// 发送失败时推送通知给管理员或用户自己
		var modem models.Modem
		database.DB.First(&modem, req.ModemID)
		label := services.ModemDisplayLabel(&modem)
		body := "发往 " + req.PhoneNumber + " 的短信发送失败：" + result.ErrMsg
		if me.IsAdmin() {
			services.Push("sms_failed", "短信发送失败", "["+label+"] "+body, "admin", nil)
		} else {
			services.Push("sms_failed", "短信发送失败", "["+label+"] "+body, "user", &me.ID)
		}
		Fail(c, http.StatusBadGateway, 502, "短信发送失败："+result.ErrMsg)
		return
	}
	OK(c, result.Message)
}

// ListMessages godoc
// @Summary 获取短信记录
// @Tags 短信
// @Produce json
// @Param modem_id query int false "设备ID过滤"
// @Param direction query string false "方向过滤(inbound/outbound)"
// @Param skip query int false "偏移量"
// @Param limit query int false "每页数量"
// @Success 200 {object} handlers.R{data=[]models.SmsMessage}
// @Security BearerAuth
// @Router /api/v1/sms/messages [get]
func ListMessages(c *gin.Context) {
	me := middleware.CurrentUser(c)
	skip, _ := strconv.Atoi(c.DefaultQuery("skip", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	svc := services.NewSmsService(database.DB)
	msgs, err := svc.ListMessages(me, c.Query("modem_id"), c.Query("direction"), skip, limit)
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	OK(c, msgs)
}

// DeleteMessage godoc
// @Summary 删除单条短信记录
// @Tags 短信
// @Produce json
// @Param id path int true "记录ID"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sms/messages/{id} [delete]
func DeleteMessage(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	svc := services.NewSmsService(database.DB)
	if err := svc.DeleteMessage(me, uint(id)); err != nil {
		switch {
		case errors.Is(err, services.ErrSmsNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		case errors.Is(err, services.ErrModemForbidden):
			Fail(c, http.StatusForbidden, 403, "无权限")
		default:
			Fail(c, http.StatusInternalServerError, 500, "删除失败")
		}
		return
	}
	OK(c, gin.H{"ok": true})
}

// batchDeleteBody 批量删除短信的请求体。
type batchDeleteBody struct {
	IDs []uint `json:"ids" binding:"required,min=1"` // 至少传一个 ID
}

// BatchDeleteMessages godoc
// @Summary 批量删除短信记录
// @Tags 短信
// @Accept json
// @Produce json
// @Param body body batchDeleteBody true "ID列表"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sms/messages/batch-delete [post]
func BatchDeleteMessages(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var body batchDeleteBody
	c.ShouldBindJSON(&body)
	svc := services.NewSmsService(database.DB)
	deleted, _ := svc.BatchDeleteMessages(me, body.IDs)
	OK(c, gin.H{"deleted": deleted})
}

// ListTemplates godoc
// @Summary 获取短信模板列表
// @Tags 短信
// @Produce json
// @Success 200 {object} handlers.R{data=[]models.SmsTemplate}
// @Security BearerAuth
// @Router /api/v1/sms/templates [get]
func ListTemplates(c *gin.Context) {
	svc := services.NewSmsService(database.DB)
	tpls, err := svc.ListTemplates()
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	OK(c, tpls)
}

// CreateTemplate godoc
// @Summary 创建短信模板
// @Tags 短信
// @Accept json
// @Produce json
// @Param body body models.SmsTemplate true "模板内容"
// @Success 200 {object} handlers.R{data=models.SmsTemplate}
// @Security BearerAuth
// @Router /api/v1/sms/templates [post]
func CreateTemplate(c *gin.Context) {
	var tpl models.SmsTemplate
	if err := c.ShouldBindJSON(&tpl); err != nil {
		Fail(c, http.StatusBadRequest, 400, "请求格式错误")
		return
	}
	svc := services.NewSmsService(database.DB)
	if err := svc.CreateTemplate(&tpl); err != nil {
		Fail(c, http.StatusInternalServerError, 500, "创建失败")
		return
	}
	OK(c, tpl)
}

// DeleteTemplate godoc
// @Summary 删除短信模板
// @Tags 短信
// @Produce json
// @Param id path int true "模板ID"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sms/templates/{id} [delete]
func DeleteTemplate(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	svc := services.NewSmsService(database.DB)
	if err := svc.DeleteTemplate(uint(id)); err != nil {
		Fail(c, http.StatusNotFound, 404, err.Error())
		return
	}
	OK(c, gin.H{"ok": true})
}

// taskToOut 将任务记录转为 API 响应，附加创建者用户名。
func taskToOut(t *models.SmsScheduledTask, svc *services.SmsService) gin.H {
	out := gin.H{}
	remarshal(t, &out)
	out["created_by_username"] = svc.GetCreatorUsername(t)
	return out
}

// ListTasks godoc
// @Summary 获取定时任务列表（当前用户）
// @Tags 短信
// @Produce json
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sms/tasks [get]
func ListTasks(c *gin.Context) {
	me := middleware.CurrentUser(c)
	svc := services.NewSmsService(database.DB)
	tasks, err := svc.ListTasks(me)
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	out := make([]gin.H, 0, len(tasks))
	for i := range tasks {
		out = append(out, taskToOut(&tasks[i], svc))
	}
	OK(c, out)
}

// taskCreate 创建定时任务的请求体。
type taskCreate struct {
	Name           string     `json:"name"            binding:"required,max=128"`
	ModemID        uint       `json:"modem_id"        binding:"required,gt=0"`
	Recipients     []string   `json:"recipients"      binding:"required,min=1"` // 至少一个收件人
	Content        string     `json:"content"         binding:"required,max=1000"`
	CronExpression *string    `json:"cron_expression"` // 与 SendOnceAt 二选一，可选
	SendOnceAt     *time.Time `json:"send_once_at"`    // UTC 时间，前端须转换，可选
}

// CreateTask godoc
// @Summary 创建定时任务
// @Tags 短信
// @Accept json
// @Produce json
// @Param body body taskCreate true "任务参数"
// @Success 200 {object} handlers.R{data=models.SmsScheduledTask}
// @Security BearerAuth
// @Router /api/v1/sms/tasks [post]
func CreateTask(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var data taskCreate
	if err := c.ShouldBindJSON(&data); err != nil {
		Fail(c, http.StatusBadRequest, 400, "请求格式错误")
		return
	}
	svc := services.NewSmsService(database.DB)
	task, err := svc.CreateTask(me, services.TaskCreateInput{
		Name:           data.Name,
		ModemID:        data.ModemID,
		Recipients:     data.Recipients,
		Content:        data.Content,
		CronExpression: data.CronExpression,
		SendOnceAt:     data.SendOnceAt,
	})
	if err != nil {
		switch {
		case errors.Is(err, services.ErrNoUsePerm):
			Fail(c, http.StatusForbidden, 403, err.Error())
		default:
			Fail(c, http.StatusBadRequest, 400, err.Error())
		}
		return
	}
	OK(c, task)
}

// taskUpdate 更新定时任务的请求体，所有字段均为可选。
type taskUpdate struct {
	Name           *string    `json:"name"            binding:"omitempty,max=128"`
	Recipients     *[]string  `json:"recipients"      binding:"omitempty,min=1"`
	Content        *string    `json:"content"         binding:"omitempty,max=1000"`
	CronExpression *string    `json:"cron_expression"`
	SendOnceAt     *time.Time `json:"send_once_at"`
	Status         *string    `json:"status"          binding:"omitempty,oneof=active paused"` // 只允许这两个值
}

// UpdateTask godoc
// @Summary 修改定时任务
// @Tags 短信
// @Accept json
// @Produce json
// @Param id path int true "任务ID"
// @Param body body taskUpdate true "修改字段"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sms/tasks/{id} [patch]
func UpdateTask(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var data taskUpdate
	c.ShouldBindJSON(&data)
	svc := services.NewSmsService(database.DB)
	task, err := svc.UpdateTask(me, uint(id), services.TaskUpdateInput{
		Name:           data.Name,
		Recipients:     data.Recipients,
		Content:        data.Content,
		CronExpression: data.CronExpression,
		SendOnceAt:     data.SendOnceAt,
		Status:         data.Status,
	})
	if err != nil {
		switch {
		case errors.Is(err, services.ErrTaskNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		case errors.Is(err, services.ErrTaskForbidden):
			Fail(c, http.StatusForbidden, 403, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "更新失败")
		}
		return
	}
	OK(c, task)
}

// DeleteTask godoc
// @Summary 删除定时任务
// @Tags 短信
// @Produce json
// @Param id path int true "任务ID"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sms/tasks/{id} [delete]
func DeleteTask(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	svc := services.NewSmsService(database.DB)
	if err := svc.DeleteTask(me, uint(id)); err != nil {
		switch {
		case errors.Is(err, services.ErrTaskNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		case errors.Is(err, services.ErrTaskForbidden):
			Fail(c, http.StatusForbidden, 403, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "删除失败")
		}
		return
	}
	OK(c, gin.H{"ok": true})
}

// RunTaskNow godoc
// @Summary 立即执行定时任务
// @Tags 短信
// @Produce json
// @Param id path int true "任务ID"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sms/tasks/{id}/run-now [post]
func RunTaskNow(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	// ExecuteTask 直接调用调度器立即执行，不经 service 层（无业务权限逻辑）
	services.ExecuteTask(uint(id))
	OK(c, gin.H{"ok": true})
}

// AdminListTasks godoc
// @Summary 管理员查看所有任务
// @Tags 短信
// @Produce json
// @Param user_id query int false "按用户过滤"
// @Param status query string false "按状态过滤"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sms/admin/tasks [get]
func AdminListTasks(c *gin.Context) {
	me := middleware.CurrentUser(c)
	svc := services.NewSmsService(database.DB)
	tasks, err := svc.AdminListTasks(me, c.Query("user_id"), c.Query("status"))
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	out := make([]gin.H, 0, len(tasks))
	for i := range tasks {
		out = append(out, taskToOut(&tasks[i], svc))
	}
	OK(c, out)
}

// AdminTaskStats godoc
// @Summary 获取任务统计数据
// @Tags 短信
// @Produce json
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sms/admin/tasks/stats [get]
func AdminTaskStats(c *gin.Context) {
	me := middleware.CurrentUser(c)
	svc := services.NewSmsService(database.DB)
	OK(c, svc.AdminTaskStats(me))
}

// AdminTaskHistory godoc
// @Summary 获取任务执行历史
// @Tags 短信
// @Produce json
// @Param id path int true "任务ID"
// @Param limit query int false "返回条数"
// @Success 200 {object} handlers.R{data=[]models.SmsMessage}
// @Security BearerAuth
// @Router /api/v1/sms/admin/tasks/{id}/history [get]
func AdminTaskHistory(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	svc := services.NewSmsService(database.DB)
	msgs, err := svc.GetTaskHistory(me, uint(id), limit)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrTaskNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		case errors.Is(err, services.ErrTaskForbidden):
			Fail(c, http.StatusForbidden, 403, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "查询失败")
		}
		return
	}
	OK(c, msgs)
}
