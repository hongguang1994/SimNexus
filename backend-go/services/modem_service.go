package services

import (
	"errors"
	"time"

	"simnexus-go/models"
	"simnexus-go/security"

	"gorm.io/gorm"
)

// ErrModemNotFound 设备不存在错误。
var ErrModemNotFound = errors.New("设备不存在")

// ErrModemForbidden 用户无权访问该设备。
var ErrModemForbidden = errors.New("无权访问该设备")

// ErrNoViewPerm 用户无 SIM 卡查看权限。
var ErrNoViewPerm = errors.New("无SIM卡查看权限")

// ModemService 提供设备管理相关的数据库操作。
type ModemService struct {
	db *gorm.DB
}

// NewModemService 创建 ModemService 实例。
func NewModemService(db *gorm.DB) *ModemService {
	return &ModemService{db: db}
}

// ModemDetail 设备详情，在 Modem 基础上附加短信统计数据。
type ModemDetail struct {
	models.Modem
	SmsSent     int64 `json:"sms_sent"`     // 历史发出总条数
	SmsReceived int64 `json:"sms_received"` // 历史收件总条数
	SmsToday    int64 `json:"sms_today"`    // 今日收发合计条数
}

// ListAvailableModems 返回所有激活设备（不限权限，用于资源库页面）。
// 调用前需在 handler 层校验 CanViewSim 权限。
func (s *ModemService) ListAvailableModems() ([]models.Modem, error) {
	var modems []models.Modem
	err := s.db.Where("is_active = ?", true).Order("id").Find(&modems).Error
	return modems, err
}

// ListUserModems 返回用户有权查看的设备列表。
// 管理员返回所有激活设备；普通用户只返回已获得访问授权的设备。
func (s *ModemService) ListUserModems(u *models.User) ([]models.Modem, error) {
	if u.IsAdmin() {
		return s.ListAvailableModems()
	}
	ids, _, ok := s.visibleModemIDs(u)
	if !ok {
		return nil, ErrNoViewPerm
	}
	if len(ids) == 0 {
		return []models.Modem{}, nil
	}
	var modems []models.Modem
	err := s.db.Where("id IN ? AND is_active = ?", ids, true).Order("id").Find(&modems).Error
	return modems, err
}

// GetModem 查询单个设备，并校验用户访问权限。
func (s *ModemService) GetModem(id uint, u *models.User) (*models.Modem, error) {
	if !u.IsAdmin() && !s.canAccessModem(u, id) {
		return nil, ErrModemForbidden
	}
	var modem models.Modem
	if s.db.First(&modem, id).Error != nil {
		return nil, ErrModemNotFound
	}
	return &modem, nil
}

// UpdateModemAlias 修改设备别名，当前仅支持修改 alias 字段。
func (s *ModemService) UpdateModemAlias(id uint, alias *string) (*models.Modem, error) {
	var modem models.Modem
	if s.db.First(&modem, id).Error != nil {
		return nil, ErrModemNotFound
	}
	if alias != nil {
		modem.Alias = *alias
	}
	s.db.Save(&modem)
	return &modem, nil
}

// GetModemDetail 查询设备详情并附加短信统计，需校验用户权限。
func (s *ModemService) GetModemDetail(id uint, u *models.User) (*ModemDetail, error) {
	if !u.IsAdmin() && !s.canAccessModem(u, id) {
		return nil, ErrModemForbidden
	}
	var modem models.Modem
	if s.db.First(&modem, id).Error != nil {
		return nil, ErrModemNotFound
	}
	var sent, received, today int64
	s.db.Model(&models.SmsMessage{}).
		Where("modem_id = ? AND direction = ?", id, models.SmsOutbound).Count(&sent)
	s.db.Model(&models.SmsMessage{}).
		Where("modem_id = ? AND direction = ?", id, models.SmsInbound).Count(&received)
	// today 统计从当天 00:00:00 UTC 开始的所有方向短信条数
	todayStart := time.Now().Truncate(24 * time.Hour)
	s.db.Model(&models.SmsMessage{}).
		Where("modem_id = ? AND created_at >= ?", id, todayStart).Count(&today)
	return &ModemDetail{
		Modem:       modem,
		SmsSent:     sent,
		SmsReceived: received,
		SmsToday:    today,
	}, nil
}

// RefreshModem 调用 ModemManager 获取设备最新状态并更新数据库。
func (s *ModemService) RefreshModem(id uint) (*models.Modem, error) {
	var modem models.Modem
	if s.db.First(&modem, id).Error != nil || modem.MmObjectPath == "" {
		return nil, ErrModemNotFound
	}
	// GetModemInfo 会根据路径区分 mmcli 设备和 ZTE 设备
	info := GetModemInfo(modem.MmObjectPath)
	if info == nil {
		return nil, errors.New("无法连接到设备")
	}
	modem.SignalQuality = info.SignalQuality
	modem.Operator = info.Operator
	modem.Status = info.Status
	s.db.Save(&modem)
	return &modem, nil
}

// visibleModemIDs 计算当前用户可见的设备 ID 列表。
// 返回值：(ids, unrestricted, permitted)
//   - ids: 允许访问的设备 ID 列表（unrestricted=true 时忽略）
//   - unrestricted: true 表示无范围限制（管理员）
//   - permitted: false 表示完全没有权限，应返回 403
func (s *ModemService) visibleModemIDs(u *models.User) ([]uint, bool, bool) {
	if u.IsAdmin() {
		return nil, true, true
	}
	p := security.Perm(u)
	if p == nil || !p.CanViewSim {
		// 用户没有任何可视 SIM 的权限
		return nil, false, false
	}
	// 获取已授权设备列表（含审批员自动拥有的设备）
	granted := security.GetUserModemGrants(s.db, u.ID, "", u)
	// 非审批员角色还需与角色的设备范围取交集
	if !p.CanApproveRequests && p.AllowedModemIDs != nil {
		filtered := granted[:0]
		for _, id := range granted {
			if security.ContainsUint(p.AllowedModemIDs, id) {
				filtered = append(filtered, id)
			}
		}
		granted = filtered
	}
	return granted, false, true
}

// canAccessModem 检查用户是否对指定设备有任意访问权限。
func (s *ModemService) canAccessModem(u *models.User, modemID uint) bool {
	if u.IsAdmin() {
		return true
	}
	ids, _, ok := s.visibleModemIDs(u)
	if !ok {
		return false
	}
	return security.ContainsUint(ids, modemID)
}
