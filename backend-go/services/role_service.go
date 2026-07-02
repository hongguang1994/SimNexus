package services

import (
	"errors"

	"simnexus-go/models"

	"gorm.io/gorm"
)

// ErrRoleNotFound 角色不存在错误。
var ErrRoleNotFound = errors.New("角色不存在")

// ErrRoleExists 角色名称重复错误。
var ErrRoleExists = errors.New("角色名称已存在")

// ErrSystemRole 系统预置角色不允许删除。
var ErrSystemRole = errors.New("系统预置角色不可删除")

// RoleService 提供角色管理相关的数据库操作。
type RoleService struct {
	db *gorm.DB
}

// NewRoleService 创建 RoleService 实例。
func NewRoleService(db *gorm.DB) *RoleService {
	return &RoleService{db: db}
}

// RoleCreateInput 创建角色的输入参数，所有布尔字段用指针表示"是否传值"。
type RoleCreateInput struct {
	Name               string  // 角色名称，必填
	Description        string  // 角色描述
	CanViewSim         *bool   // 是否可查看 SIM 卡
	CanApproveRequests *bool   // 是否可审批 SIM 申请
	CanViewHistory     *bool   // 是否可查看短信记录
	ReadOnly           *bool   // 是否只读（不可发短信）
	CanSupport         *bool   // 是否为客服角色
	AllowedModemIDs    *[]uint // 可管理的设备 ID 列表，nil 表示不限制
}

// RoleUpdateInput 更新角色的输入参数，nil 字段表示不修改该项。
type RoleUpdateInput struct {
	Name               string
	Description        *string // 用指针区分"未传"与"传空字符串"
	CanViewSim         *bool
	CanApproveRequests *bool
	CanViewHistory     *bool
	ReadOnly           *bool
	CanSupport         *bool
	AllowedModemIDs    *[]uint // nil=不修改范围；传入后替换设备关联
	HasScopeField      bool    // 请求体是否包含 allowed_modem_ids 字段（用于区分"未传"与"传空"）
}

// ListRoles 查询所有角色，预加载设备范围关联，按 ID 升序。
func (s *RoleService) ListRoles() ([]models.Role, error) {
	var roles []models.Role
	err := s.db.Preload("ModemScope").Order("id").Find(&roles).Error
	return roles, err
}

// GetRoleByID 按 ID 查询角色，预加载 ModemScope。
func (s *RoleService) GetRoleByID(id uint) (*models.Role, error) {
	var role models.Role
	if err := s.db.Preload("ModemScope").First(&role, id).Error; err != nil {
		return nil, ErrRoleNotFound
	}
	return &role, nil
}

// CreateRole 创建新角色，校验角色名不重复。
func (s *RoleService) CreateRole(input RoleCreateInput) (*models.Role, error) {
	// 角色名唯一性检查
	var existing models.Role
	if s.db.Where("name = ?", input.Name).First(&existing).Error == nil {
		return nil, ErrRoleExists
	}
	role := models.Role{
		Name:               input.Name,
		Description:        input.Description,
		CanViewSim:         derefBoolSvc(input.CanViewSim),
		CanApproveRequests: derefBoolSvc(input.CanApproveRequests),
		CanViewHistory:     derefBoolSvc(input.CanViewHistory),
		ReadOnly:           derefBoolSvc(input.ReadOnly),
		CanSupport:         derefBoolSvc(input.CanSupport),
	}
	if err := s.db.Create(&role).Error; err != nil {
		return nil, err
	}
	// 设置设备范围关联（多对多）
	s.applyModemScope(&role, input.AllowedModemIDs)
	return &role, nil
}

// UpdateRole 按字段更新角色，只更新请求体中实际传入的字段。
func (s *RoleService) UpdateRole(id uint, input RoleUpdateInput) (*models.Role, error) {
	role, err := s.GetRoleByID(id)
	if err != nil {
		return nil, err
	}
	if input.Name != "" {
		role.Name = input.Name
	}
	if input.Description != nil {
		role.Description = *input.Description
	}
	if input.CanViewSim != nil {
		role.CanViewSim = *input.CanViewSim
	}
	if input.CanApproveRequests != nil {
		role.CanApproveRequests = *input.CanApproveRequests
	}
	if input.CanViewHistory != nil {
		role.CanViewHistory = *input.CanViewHistory
	}
	if input.ReadOnly != nil {
		role.ReadOnly = *input.ReadOnly
	}
	if input.CanSupport != nil {
		role.CanSupport = *input.CanSupport
	}
	s.db.Save(role)
	// 仅当请求体中包含 allowed_modem_ids 字段时才更新设备范围
	if input.HasScopeField {
		s.applyModemScope(role, input.AllowedModemIDs)
	}
	return role, nil
}

// DeleteRole 删除角色，系统预置角色（is_system=true）不可删除。
func (s *RoleService) DeleteRole(id uint) error {
	role, err := s.GetRoleByID(id)
	if err != nil {
		return err
	}
	if role.IsSystem {
		return ErrSystemRole
	}
	return s.db.Delete(role).Error
}

// applyModemScope 替换角色的设备范围多对多关联。
// ids=nil 或空切片时清空所有关联（表示不限制）。
func (s *RoleService) applyModemScope(role *models.Role, ids *[]uint) {
	if ids == nil || len(*ids) == 0 {
		// 清除所有关联设备，角色变为无范围限制
		s.db.Model(role).Association("ModemScope").Clear()
		role.ModemScope = nil
		return
	}
	var modems []models.Modem
	s.db.Where("id IN ?", *ids).Find(&modems)
	// Replace 会先删除旧关联再批量插入新关联，保证原子性
	s.db.Model(role).Association("ModemScope").Replace(modems)
	role.ModemScope = modems
}

// derefBoolSvc 安全解引用 *bool，nil 时返回 false（仅在 services 包内使用）。
func derefBoolSvc(b *bool) bool {
	return b != nil && *b
}
