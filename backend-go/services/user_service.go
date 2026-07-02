// Package services 包含业务逻辑层，handler 只负责 HTTP 参数解析和响应格式化，
// 所有数据库操作和业务规则都在此层实现。
package services

import (
	"errors"

	"simnexus-go/models"
	"simnexus-go/security"

	"gorm.io/gorm"
)

// ErrUserNotFound 用户不存在错误。
var ErrUserNotFound = errors.New("用户不存在")

// ErrUserExists 用户名已存在错误。
var ErrUserExists = errors.New("用户名已存在")

// ErrPasswordTooShort 密码长度不足错误。
var ErrPasswordTooShort = errors.New("密码至少 6 位")

// ErrWrongPassword 原密码错误。
var ErrWrongPassword = errors.New("原密码错误")

// ErrCannotDeleteSelf 不能删除自己的账号。
var ErrCannotDeleteSelf = errors.New("不能删除自己")

// UserService 提供用户管理相关的数据库操作。
type UserService struct {
	db *gorm.DB
}

// NewUserService 创建 UserService 实例，注入数据库连接。
func NewUserService(db *gorm.DB) *UserService {
	return &UserService{db: db}
}

// ListUsers 查询所有用户，并预加载 RBAC 角色列表，按 ID 升序排列。
func (s *UserService) ListUsers() ([]models.User, error) {
	var users []models.User
	err := s.db.Preload("RbacRoles").Order("id").Find(&users).Error
	return users, err
}

// GetUserByID 按 ID 查询用户，并预加载 RBAC 角色。
func (s *UserService) GetUserByID(id uint) (*models.User, error) {
	var user models.User
	if err := s.db.Preload("RbacRoles").First(&user, id).Error; err != nil {
		return nil, ErrUserNotFound
	}
	return &user, nil
}

// CreateUser 创建新用户。
// 校验：用户名唯一、密码不少于 6 位。
// role 为空时默认为普通用户（"user"）。
func (s *UserService) CreateUser(username, password, role string) (*models.User, error) {
	// 检查用户名是否已存在
	var existing models.User
	if s.db.Where("username = ?", username).First(&existing).Error == nil {
		return nil, ErrUserExists
	}
	if len(password) < 6 {
		return nil, ErrPasswordTooShort
	}
	if role == "" {
		role = models.RoleUser
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return nil, err
	}
	user := models.User{
		Username:     username,
		PasswordHash: hash,
		Role:         role,
		IsActive:     true,
	}
	if err := s.db.Create(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// UpdateUser 更新用户的角色和启用状态（均为可选字段，nil 表示不修改）。
func (s *UserService) UpdateUser(id uint, role *string, isActive *bool) (*models.User, error) {
	user, err := s.GetUserByID(id)
	if err != nil {
		return nil, err
	}
	if role != nil {
		user.Role = *role
	}
	if isActive != nil {
		user.IsActive = *isActive
	}
	s.db.Save(user)
	return user, nil
}

// DeleteUser 删除用户。
// 校验：不能删除自身账号（通过 selfID 传入当前用户 ID 比较）。
func (s *UserService) DeleteUser(id uint, selfID uint) error {
	if id == selfID {
		return ErrCannotDeleteSelf
	}
	var user models.User
	if s.db.First(&user, id).Error != nil {
		return ErrUserNotFound
	}
	return s.db.Delete(&user).Error
}

// ResetPassword 管理员重置指定用户的密码，密码不少于 6 位。
func (s *UserService) ResetPassword(id uint, newPassword string) (*models.User, error) {
	if len(newPassword) < 6 {
		return nil, ErrPasswordTooShort
	}
	user, err := s.GetUserByID(id)
	if err != nil {
		return nil, err
	}
	hash, err := security.HashPassword(newPassword)
	if err != nil {
		return nil, err
	}
	user.PasswordHash = hash
	s.db.Save(user)
	return user, nil
}

// ChangePassword 用户修改自己的密码，需验证旧密码。
func (s *UserService) ChangePassword(user *models.User, oldPassword, newPassword string) error {
	// 验证旧密码是否正确
	if !security.VerifyPassword(oldPassword, user.PasswordHash) {
		return ErrWrongPassword
	}
	if len(newPassword) < 6 {
		return ErrPasswordTooShort
	}
	hash, err := security.HashPassword(newPassword)
	if err != nil {
		return err
	}
	user.PasswordHash = hash
	return s.db.Save(user).Error
}

// SetUserRoles 替换用户的 RBAC 角色列表。
// roleIDs 为空时清空所有角色。返回实际生效的角色 ID 列表。
func (s *UserService) SetUserRoles(userID uint, roleIDs []uint) ([]uint, error) {
	var user models.User
	if s.db.First(&user, userID).Error != nil {
		return nil, ErrUserNotFound
	}
	var roles []models.Role
	if len(roleIDs) > 0 {
		s.db.Where("id IN ?", roleIDs).Find(&roles)
	}
	// Replace 会先清空旧关联，再批量插入新关联
	s.db.Model(&user).Association("RbacRoles").Replace(roles)
	ids := make([]uint, 0, len(roles))
	for _, r := range roles {
		ids = append(ids, r.ID)
	}
	return ids, nil
}
