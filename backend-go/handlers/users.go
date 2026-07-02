package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"simnexus-go/database"
	"simnexus-go/middleware"
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
	svc := services.NewUserService(database.DB)
	users, err := svc.ListUsers()
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	out := make([]gin.H, 0, len(users))
	for i := range users {
		out = append(out, userOut(&users[i]))
	}
	OK(c, out)
}

// userCreate 创建用户的请求体。
type userCreate struct {
	Username string `json:"username" binding:"required,min=2,max=64"`
	Password string `json:"password" binding:"required,min=6"`
	Role     string `json:"role"`    // 可选，默认为 user
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
	svc := services.NewUserService(database.DB)
	user, err := svc.CreateUser(data.Username, data.Password, data.Role)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrUserExists):
			Fail(c, http.StatusBadRequest, 400, err.Error())
		case errors.Is(err, services.ErrPasswordTooShort):
			Fail(c, http.StatusBadRequest, 400, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "创建失败")
		}
		return
	}
	// 发送通知给管理员
	services.Push("new_user", "新用户注册",
		"新用户 "+user.Username+" 已创建（角色："+user.Role+"）", "admin", nil)
	OK(c, userOut(user))
}

// userUpdate 修改用户的请求体，字段均为可选。
type userUpdate struct {
	Role     *string `json:"role"`      // 可选，admin 或 user
	IsActive *bool   `json:"is_active"` // 可选，true=启用 false=禁用
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
	var data userUpdate
	c.ShouldBindJSON(&data)
	svc := services.NewUserService(database.DB)
	user, err := svc.UpdateUser(uint(id), data.Role, data.IsActive)
	if err != nil {
		Fail(c, http.StatusNotFound, 404, err.Error())
		return
	}
	OK(c, userOut(user))
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
	svc := services.NewUserService(database.DB)
	if err := svc.DeleteUser(uint(id), me.ID); err != nil {
		switch {
		case errors.Is(err, services.ErrCannotDeleteSelf):
			Fail(c, http.StatusBadRequest, 400, err.Error())
		case errors.Is(err, services.ErrUserNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "删除失败")
		}
		return
	}
	OK(c, gin.H{"ok": true})
}

// passwordReset 管理员重置密码请求体。
type passwordReset struct {
	NewPassword string `json:"new_password" binding:"required,min=6"`
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
	var data passwordReset
	c.ShouldBindJSON(&data)
	svc := services.NewUserService(database.DB)
	user, err := svc.ResetPassword(uint(id), data.NewPassword)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrUserNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		case errors.Is(err, services.ErrPasswordTooShort):
			Fail(c, http.StatusBadRequest, 400, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "重置失败")
		}
		return
	}
	OK(c, userOut(user))
}

// passwordChange 用户修改自己密码的请求体。
type passwordChange struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
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
	svc := services.NewUserService(database.DB)
	if err := svc.ChangePassword(me, data.OldPassword, data.NewPassword); err != nil {
		switch {
		case errors.Is(err, services.ErrWrongPassword):
			Fail(c, http.StatusBadRequest, 400, err.Error())
		case errors.Is(err, services.ErrPasswordTooShort):
			Fail(c, http.StatusBadRequest, 400, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "修改失败")
		}
		return
	}
	OK(c, gin.H{"ok": true})
}

// setRolesBody 设置用户角色的请求体。
type setRolesBody struct {
	RoleIDs []uint `json:"role_ids" binding:"required"` // 传空数组 [] 表示清空角色
}

// SetUserRoles godoc
// @Summary 设置用户角色
// @Tags 角色管理
// @Accept json
// @Produce json
// @Param id path int true "用户ID"
// @Param body body setRolesBody true "角色ID列表"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/roles/users/{id}/roles [put]
func SetUserRoles(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var body setRolesBody
	c.ShouldBindJSON(&body)
	svc := services.NewUserService(database.DB)
	roleIDs, err := svc.SetUserRoles(uint(id), body.RoleIDs)
	if err != nil {
		Fail(c, http.StatusNotFound, 404, err.Error())
		return
	}
	OK(c, gin.H{"ok": true, "user_id": id, "role_ids": roleIDs})
}
