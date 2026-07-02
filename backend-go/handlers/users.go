package handlers

import (
	"net/http"
	"strconv"

	"simnexus-go/database"
	"simnexus-go/middleware"
	"simnexus-go/models"
	"simnexus-go/security"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// ListUsers godoc
// @Summary 获取用户列表
// @Tags 用户管理
// @Produce json
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/users/ [get]
func ListUsers(c *gin.Context) {
	var users []models.User
	database.DB.Preload("RbacRoles").Order("id").Find(&users)
	out := make([]gin.H, 0, len(users))
	for i := range users {
		out = append(out, userOut(&users[i]))
	}
	OK(c, out)
}

type userCreate struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// CreateUser godoc
// @Summary 创建用户
// @Tags 用户管理
// @Accept json
// @Produce json
// @Param body body userCreate true "用户信息"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/users/ [post]
func CreateUser(c *gin.Context) {
	var data userCreate
	if err := c.ShouldBindJSON(&data); err != nil {
		Fail(c, http.StatusBadRequest, 400, "请求格式错误")
		return
	}
	var existing models.User
	if database.DB.Where("username = ?", data.Username).First(&existing).Error == nil {
		Fail(c, http.StatusBadRequest, 400, "用户名已存在")
		return
	}
	if len(data.Password) < 6 {
		Fail(c, http.StatusBadRequest, 400, "密码至少 6 位")
		return
	}
	role := data.Role
	if role == "" {
		role = models.RoleUser
	}
	hash, _ := security.HashPassword(data.Password)
	user := models.User{Username: data.Username, PasswordHash: hash, Role: role, IsActive: true}
	database.DB.Create(&user)
	services.Push("new_user", "新用户注册", "新用户 "+user.Username+" 已创建（角色："+user.Role+"）", "admin", nil)
	OK(c, userOut(&user))
}

type userUpdate struct {
	Role     *string `json:"role"`
	IsActive *bool   `json:"is_active"`
}

// UpdateUser godoc
// @Summary 修改用户信息
// @Tags 用户管理
// @Accept json
// @Produce json
// @Param id path int true "用户ID"
// @Param body body userUpdate true "修改字段"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/users/{id} [patch]
func UpdateUser(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var user models.User
	if database.DB.Preload("RbacRoles").First(&user, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "用户不存在")
		return
	}
	var data userUpdate
	c.ShouldBindJSON(&data)
	if data.Role != nil {
		user.Role = *data.Role
	}
	if data.IsActive != nil {
		user.IsActive = *data.IsActive
	}
	database.DB.Save(&user)
	OK(c, userOut(&user))
}

// DeleteUser godoc
// @Summary 删除用户
// @Tags 用户管理
// @Produce json
// @Param id path int true "用户ID"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/users/{id} [delete]
func DeleteUser(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	me := middleware.CurrentUser(c)
	if uint(id) == me.ID {
		Fail(c, http.StatusBadRequest, 400, "不能删除自己")
		return
	}
	var user models.User
	if database.DB.First(&user, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "用户不存在")
		return
	}
	database.DB.Delete(&user)
	OK(c, gin.H{"ok": true})
}

type passwordReset struct {
	NewPassword string `json:"new_password"`
}

// ResetPassword godoc
// @Summary 管理员重置用户密码
// @Tags 用户管理
// @Accept json
// @Produce json
// @Param id path int true "用户ID"
// @Param body body passwordReset true "新密码"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/users/{id}/reset-password [post]
func ResetPassword(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var user models.User
	if database.DB.Preload("RbacRoles").First(&user, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "用户不存在")
		return
	}
	var data passwordReset
	c.ShouldBindJSON(&data)
	if len(data.NewPassword) < 6 {
		Fail(c, http.StatusBadRequest, 400, "密码至少 6 位")
		return
	}
	user.PasswordHash, _ = security.HashPassword(data.NewPassword)
	database.DB.Save(&user)
	OK(c, userOut(&user))
}

type passwordChange struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// ChangePassword godoc
// @Summary 修改当前用户密码
// @Tags 用户管理
// @Accept json
// @Produce json
// @Param body body passwordChange true "旧密码和新密码"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/users/me/change-password [post]
func ChangePassword(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var data passwordChange
	c.ShouldBindJSON(&data)
	if !security.VerifyPassword(data.OldPassword, me.PasswordHash) {
		Fail(c, http.StatusBadRequest, 400, "原密码错误")
		return
	}
	if len(data.NewPassword) < 6 {
		Fail(c, http.StatusBadRequest, 400, "密码至少 6 位")
		return
	}
	me.PasswordHash, _ = security.HashPassword(data.NewPassword)
	database.DB.Save(me)
	OK(c, gin.H{"ok": true})
}
