package services

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"simnexus-go/models"
	"simnexus-go/security"

	"gorm.io/gorm"
)

// reModemSvc 从 D-Bus 路径中提取 mmcli 设备索引（如 /Modem/3 → "3"）。
var reModemSvc = regexp.MustCompile(`/Modem/(\d+)$`)

// ErrSmsNotFound 短信记录不存在。
var ErrSmsNotFound = errors.New("记录不存在")

// ErrTemplateNotFound 模板不存在。
var ErrTemplateNotFound = errors.New("模板不存在")

// ErrTaskNotFound 定时任务不存在。
var ErrTaskNotFound = errors.New("任务不存在")

// ErrTaskForbidden 无权操作该任务。
var ErrTaskForbidden = errors.New("无权操作此任务")

// ErrNoUsePerm 无该 SIM 卡的使用权限。
var ErrNoUsePerm = errors.New("无该SIM卡的使用权限，请先申请")

// ErrModemUnavailable 设备不可用（路径无效或无法连接）。
var ErrModemUnavailable = errors.New("设备不可用")

// SmsService 提供短信收发、模板、定时任务相关的数据库操作和业务逻辑。
type SmsService struct {
	db *gorm.DB
}

// NewSmsService 创建 SmsService 实例。
func NewSmsService(db *gorm.DB) *SmsService {
	return &SmsService{db: db}
}

// SendResult 发送短信的执行结果。
type SendResult struct {
	Message *models.SmsMessage // 入库的短信记录
	Success bool               // 是否发送成功
	ErrMsg  string             // 失败时的错误原因
}

// SendSMS 立即发送一条短信，并将结果写入 sms_messages 表。
// 校验：用户必须拥有该 SIM 卡的 use 级别授权。
func (s *SmsService) SendSMS(u *models.User, modemID uint, phone, content string) (*SendResult, error) {
	// 校验 SIM 卡使用权限
	if !s.requireUseGrant(u, modemID) {
		return nil, ErrNoUsePerm
	}
	// PDU 号码格式不允许空格等分隔符，去除后再发送（modem manager 会拒绝含空格的号码）
	phone = strings.Join(strings.Fields(phone), "")
	var modem models.Modem
	if s.db.First(&modem, modemID).Error != nil {
		return nil, ErrModemNotFound
	}
	// 根据路径前缀区分 ZTE 设备和标准 mmcli 设备
	obj := modem.MmObjectPath
	var success bool
	var errMsg string
	if modem.VowifiMode {
		// 该卡处于 VoWiFi 模式：走常驻 VoWiFi 会话发短信（同一会话也负责接收 MT）
		success, errMsg = GetVowifiManager().SendSMS(&modem, phone, content)
	} else if strings.HasPrefix(obj, "zte:") {
		// ZTE 便携 WiFi 设备通过 HTTP goform 接口发送
		success = ZteSendSMS(phone, content)
		if !success {
			errMsg = "ZTE device returned failure"
		}
	} else {
		// 标准 ModemManager 设备：从 D-Bus 路径提取数字索引
		m := reModemSvc.FindStringSubmatch(obj)
		if m == nil {
			return nil, ErrModemUnavailable
		}
		success, errMsg = SendSMS(m[1], phone, content)
	}

	// 构建短信记录并入库，无论成功失败都保留记录
	channel := models.SmsChannelCellular
	if modem.VowifiMode {
		channel = models.SmsChannelVowifi
	}
	now := time.Now()
	msg := &models.SmsMessage{
		ModemID:     modem.ID,
		Direction:   models.SmsOutbound,
		Channel:     channel,
		PhoneNumber: phone,
		Content:     content,
		Status:      models.SmsSent,
		CreatedByID: &u.ID,
	}
	if success {
		msg.SentAt = &now
	} else {
		msg.Status = models.SmsFailed
		msg.ErrorMessage = &errMsg
	}
	s.db.Create(msg)
	broadcastMessage(msg) // WebSocket 实时推送
	return &SendResult{Message: msg, Success: success, ErrMsg: errMsg}, nil
}

// ListMessages 查询短信记录，按用户权限过滤可见设备。
// 支持 modem_id / direction 过滤和分页（skip/limit）。
func (s *SmsService) ListMessages(u *models.User, modemID, direction string, skip, limit int) ([]models.SmsMessage, error) {
	q := s.db.Model(&models.SmsMessage{})
	// 非管理员只能查看自己有权限设备的短信
	ids, unrestricted := s.userVisibleModemIDs(u)
	if !unrestricted {
		if len(ids) == 0 {
			return []models.SmsMessage{}, nil
		}
		q = q.Where("modem_id IN ?", ids)
	}
	if modemID != "" {
		q = q.Where("modem_id = ?", modemID)
	}
	if direction != "" {
		q = q.Where("direction = ?", direction)
	}
	var msgs []models.SmsMessage
	err := q.Order("created_at desc").Offset(skip).Limit(limit).Find(&msgs).Error
	return msgs, err
}

// DeleteMessage 删除单条短信记录，同时从物理设备上删除收件短信。
func (s *SmsService) DeleteMessage(u *models.User, id uint) error {
	var msg models.SmsMessage
	if s.db.First(&msg, id).Error != nil {
		return ErrSmsNotFound
	}
	// 校验用户是否有权看到该短信所属的设备
	ids, unrestricted := s.userVisibleModemIDs(u)
	if !unrestricted && !security.ContainsUint(ids, msg.ModemID) {
		return ErrModemForbidden
	}
	s.deleteFromModem(&msg)
	return s.db.Delete(&msg).Error
}

// BatchDeleteMessages 批量删除短信记录，只删除用户有权限设备下的记录。
// 返回实际删除数量。
func (s *SmsService) BatchDeleteMessages(u *models.User, ids []uint) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	q := s.db.Where("id IN ?", ids)
	visibleIDs, unrestricted := s.userVisibleModemIDs(u)
	if !unrestricted {
		// 非管理员只能删除自己有权限设备下的短信
		q = q.Where("modem_id IN ?", visibleIDs)
	}
	var msgs []models.SmsMessage
	q.Find(&msgs)
	for i := range msgs {
		s.deleteFromModem(&msgs[i])
		s.db.Delete(&msgs[i])
	}
	return len(msgs), nil
}

// ListTemplates 查询所有短信模板。
func (s *SmsService) ListTemplates() ([]models.SmsTemplate, error) {
	var tpls []models.SmsTemplate
	err := s.db.Find(&tpls).Error
	return tpls, err
}

// CreateTemplate 创建短信模板。
func (s *SmsService) CreateTemplate(tpl *models.SmsTemplate) error {
	return s.db.Create(tpl).Error
}

// DeleteTemplate 删除短信模板，模板不存在时返回 ErrTemplateNotFound。
func (s *SmsService) DeleteTemplate(id uint) error {
	var tpl models.SmsTemplate
	if s.db.First(&tpl, id).Error != nil {
		return ErrTemplateNotFound
	}
	return s.db.Delete(&tpl).Error
}

// TaskCreateInput 创建定时任务的输入参数。
type TaskCreateInput struct {
	Name           string     // 任务名称
	ModemID        uint       // 使用的 SIM 卡 ID
	Recipients     []string   // 收件人号码列表
	Content        string     // 短信内容
	CronExpression *string    // Cron 表达式（与 SendOnceAt 二选一）
	SendOnceAt     *time.Time // 单次发送时间（UTC），与 CronExpression 二选一
}

// CreateTask 创建定时短信任务，并注册到调度器。
// 校验：用户需拥有目标 SIM 卡的 use 级别权限。
func (s *SmsService) CreateTask(u *models.User, input TaskCreateInput) (*models.SmsScheduledTask, error) {
	if !s.requireUseGrant(u, input.ModemID) {
		return nil, ErrNoUsePerm
	}
	var cron *string
	if input.CronExpression != nil {
		t := strings.TrimSpace(*input.CronExpression)
		if t != "" {
			cron = &t
		}
	}
	// 必须提供 cron 或单次发送时间中的一个
	if cron == nil && input.SendOnceAt == nil {
		return nil, errors.New("需要提供 cron_expression 或 send_once_at")
	}
	task := models.SmsScheduledTask{
		Name:           input.Name,
		ModemID:        input.ModemID,
		Recipients:     models.JSONList(input.Recipients),
		Content:        input.Content,
		CronExpression: cron,
		SendOnceAt:     input.SendOnceAt,
		Status:         models.TaskActive,
		CreatedByID:    &u.ID,
	}
	s.db.Create(&task)
	// 将任务注册到 APScheduler 风格的 Go 调度器
	ScheduleTask(task)
	return &task, nil
}

// TaskUpdateInput 更新定时任务的可选字段，nil 表示不修改。
type TaskUpdateInput struct {
	Name           *string
	Recipients     *[]string
	Content        *string
	CronExpression *string
	SendOnceAt     *time.Time
	Status         *string
}

// UpdateTask 更新任务字段并重新注册调度。
// 非管理员只能修改自己创建的任务。
func (s *SmsService) UpdateTask(u *models.User, id uint, input TaskUpdateInput) (*models.SmsScheduledTask, error) {
	var task models.SmsScheduledTask
	if s.db.First(&task, id).Error != nil {
		return nil, ErrTaskNotFound
	}
	// 权限检查：非管理员只能操作自己的任务
	if !u.IsAdmin() && (task.CreatedByID == nil || *task.CreatedByID != u.ID) {
		return nil, ErrTaskForbidden
	}
	if input.Name != nil {
		task.Name = *input.Name
	}
	if input.Recipients != nil {
		task.Recipients = models.JSONList(*input.Recipients)
	}
	if input.Content != nil {
		task.Content = *input.Content
	}
	if input.CronExpression != nil {
		task.CronExpression = input.CronExpression
	}
	if input.SendOnceAt != nil {
		task.SendOnceAt = input.SendOnceAt
	}
	if input.Status != nil {
		task.Status = *input.Status
	}
	s.db.Save(&task)
	// 先移除旧调度，再根据状态决定是否重新注册
	RemoveTask(task.ID)
	if task.Status == models.TaskActive {
		ScheduleTask(task)
	}
	return &task, nil
}

// DeleteTask 删除任务并从调度器中移除。
func (s *SmsService) DeleteTask(u *models.User, id uint) error {
	var task models.SmsScheduledTask
	if s.db.First(&task, id).Error != nil {
		return ErrTaskNotFound
	}
	if !u.IsAdmin() && (task.CreatedByID == nil || *task.CreatedByID != u.ID) {
		return ErrTaskForbidden
	}
	RemoveTask(task.ID)
	return s.db.Delete(&task).Error
}

// ListTasks 查询定时任务列表，管理员可见所有，普通用户只见自己的。
func (s *SmsService) ListTasks(u *models.User) ([]models.SmsScheduledTask, error) {
	q := s.db.Model(&models.SmsScheduledTask{})
	if !u.IsAdmin() {
		ids, unrestricted := s.userVisibleModemIDs(u)
		if !unrestricted {
			q = q.Where("modem_id IN ?", ids)
		}
		// 普通用户只能看到自己创建的任务
		q = q.Where("created_by_id = ?", u.ID)
	}
	var tasks []models.SmsScheduledTask
	err := q.Order("id desc").Find(&tasks).Error
	return tasks, err
}

// AdminListTasks 管理员任务列表，支持按 user_id / status 过滤。
// 非管理员调用时退化为只看自己的任务。
func (s *SmsService) AdminListTasks(u *models.User, userID, status string) ([]models.SmsScheduledTask, error) {
	q := s.db.Model(&models.SmsScheduledTask{})
	if !u.IsAdmin() {
		q = q.Where("created_by_id = ?", u.ID)
	} else if userID != "" {
		q = q.Where("created_by_id = ?", userID)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var tasks []models.SmsScheduledTask
	err := q.Order("id desc").Find(&tasks).Error
	return tasks, err
}

// AdminTaskStats 统计各状态任务数量。
// 非管理员只统计自己的任务。
func (s *SmsService) AdminTaskStats(u *models.User) map[string]interface{} {
	q := s.db.Model(&models.SmsScheduledTask{})
	if !u.IsAdmin() {
		q = q.Where("created_by_id = ?", u.ID)
	}
	var tasks []models.SmsScheduledTask
	q.Find(&tasks)
	stats := map[string]interface{}{
		"total": len(tasks), "active": 0, "paused": 0, "completed": 0, "failed": 0,
	}
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
	return stats
}

// GetTaskHistory 获取任务的短信执行历史记录。
// 非管理员只能查看自己的任务历史。
func (s *SmsService) GetTaskHistory(u *models.User, taskID uint, limit int) ([]models.SmsMessage, error) {
	var task models.SmsScheduledTask
	if s.db.First(&task, taskID).Error != nil {
		return nil, ErrTaskNotFound
	}
	if !u.IsAdmin() && (task.CreatedByID == nil || *task.CreatedByID != u.ID) {
		return nil, ErrTaskForbidden
	}
	var msgs []models.SmsMessage
	err := s.db.Where("scheduled_task_id = ?", taskID).
		Order("created_at desc").Limit(limit).Find(&msgs).Error
	return msgs, err
}

// GetCreatorUsername 查询任务创建者的用户名（用于任务列表展示）。
func (s *SmsService) GetCreatorUsername(task *models.SmsScheduledTask) string {
	if task.CreatedByID == nil {
		return ""
	}
	var u models.User
	if s.db.First(&u, *task.CreatedByID).Error != nil {
		return ""
	}
	return u.Username
}

// ModemDisplayLabel 返回设备展示名称，优先级：别名 > 型号 > 设备#ID。
func ModemDisplayLabel(m *models.Modem) string {
	if m.Alias != "" {
		return m.Alias
	}
	if m.Model != "" {
		return m.Model
	}
	return "设备#" + strconv.Itoa(int(m.ID))
}

// requireUseGrant 检查用户是否对指定设备拥有 use 级别权限（可发送短信）。
func (s *SmsService) requireUseGrant(u *models.User, modemID uint) bool {
	if u.IsAdmin() {
		return true
	}
	useIDs := security.GetUserModemGrants(s.db, u.ID, models.LevelUse, u)
	return security.ContainsUint(useIDs, modemID)
}

// userVisibleModemIDs 返回用户可见的设备 ID 列表。
// unrestricted=true 表示管理员，可查看所有设备。
func (s *SmsService) userVisibleModemIDs(u *models.User) ([]uint, bool) {
	if u.IsAdmin() {
		return nil, true
	}
	return security.GetUserModemGrants(s.db, u.ID, "", u), false
}

// deleteFromModem 将收件短信从物理设备上删除，防止设备存储满溢。
// 只处理收件方向且有 mm_sms_index 的记录（ZTE 设备和出件不需要删除）。
func (s *SmsService) deleteFromModem(msg *models.SmsMessage) {
	if msg.Direction != models.SmsInbound || msg.MmSmsIndex == "" {
		return
	}
	var modem models.Modem
	if s.db.First(&modem, msg.ModemID).Error == nil {
		// 调用 modem_manager.go 中的 DeleteSmsFromModem
		DeleteSmsFromModem(modem.MmObjectPath, msg.MmSmsIndex)
	}
}
