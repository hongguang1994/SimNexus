package handlers

import (
	"net/http"
	"strconv"

	"simnexus-go/database"
	"simnexus-go/models"

	"github.com/gin-gonic/gin"
)

// roleBody 创建/更新角色的请求体，布尔字段使用指针以区分"未传"与"传 false"。
type roleBody struct {
	Name               string  `json:"name"`
	Description        string  `json:"description"`
	CanViewSim         *bool   `json:"can_view_sim"`
	CanApproveRequests *bool   `json:"can_approve_requests"`
	CanViewHistory     *bool   `json:"can_view_history"`
	ReadOnly           *bool   `json:"read_only"`
	CanSupport         *bool   `json:"can_support"`
	AllowedModemIDs    *[]uint `json:"allowed_modem_ids"` // nil=不修改，[]uint{}=清除限制
	// rawHasScope 内部标记，记录请求体是否包含 allowed_modem_ids 字段。
	rawHasScope bool
}

// ListRoles godoc
// @Summary 获取角色列表
// @Tags 角色管理
// @Produce json
// @Success 200 {object} handlers.R{data=[]models.Role}
// @Security BearerAuth
// @Router /api/v1/roles/ [get]
func ListRoles(c *gin.Context) {
	var roles []models.Role
	database.DB.Preload("ModemScope").Order("id").Find(&roles)
	out := make([]map[string]interface{}, 0, len(roles))
	for _, r := range roles {
		out = append(out, models.RoleOut(r))
	}
	OK(c, out)
}

// applyModemScope 替换角色的设备范围关联；ids=nil 或空时清除所有关联。
func applyModemScope(role *models.Role, ids *[]uint) {
	if ids == nil || len(*ids) == 0 {
		database.DB.Model(role).Association("ModemScope").Clear()
		role.ModemScope = nil
		return
	}
	var modems []models.Modem
	database.DB.Where("id IN ?", *ids).Find(&modems)
	database.DB.Model(role).Association("ModemScope").Replace(modems)
	role.ModemScope = modems
}

// CreateRole godoc
// @Summary 创建角色
// @Tags 角色管理
// @Accept json
// @Produce json
// @Param body body roleBody true "角色信息"
// @Success 200 {object} handlers.R{data=models.Role}
// @Security BearerAuth
// @Router /api/v1/roles/ [post]
func CreateRole(c *gin.Context) {
	var raw map[string]interface{}
	c.ShouldBindJSON(&raw)
	var body roleBody
	remarshal(raw, &body)

	var existing models.Role
	if database.DB.Where("name = ?", body.Name).First(&existing).Error == nil {
		Fail(c, http.StatusBadRequest, 400, "角色名称已存在")
		return
	}
	role := models.Role{
		Name:               body.Name,
		Description:        body.Description,
		CanViewSim:         derefBool(body.CanViewSim),
		CanApproveRequests: derefBool(body.CanApproveRequests),
		CanViewHistory:     derefBool(body.CanViewHistory),
		ReadOnly:           derefBool(body.ReadOnly),
		CanSupport:         derefBool(body.CanSupport),
	}
	database.DB.Create(&role)
	applyModemScope(&role, body.AllowedModemIDs)
	OK(c, models.RoleOut(role))
}

// UpdateRole godoc
// @Summary 修改角色
// @Tags 角色管理
// @Accept json
// @Produce json
// @Param id path int true "角色ID"
// @Param body body roleBody true "修改字段"
// @Success 200 {object} handlers.R{data=models.Role}
// @Security BearerAuth
// @Router /api/v1/roles/{id} [patch]
func UpdateRole(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var role models.Role
	if database.DB.Preload("ModemScope").First(&role, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "角色不存在")
		return
	}
	var raw map[string]interface{}
	c.ShouldBindJSON(&raw)
	var body roleBody
	remarshal(raw, &body)

	if body.Name != "" {
		role.Name = body.Name
	}
	if _, ok := raw["description"]; ok {
		role.Description = body.Description
	}
	if body.CanViewSim != nil {
		role.CanViewSim = *body.CanViewSim
	}
	if body.CanApproveRequests != nil {
		role.CanApproveRequests = *body.CanApproveRequests
	}
	if body.CanViewHistory != nil {
		role.CanViewHistory = *body.CanViewHistory
	}
	if body.ReadOnly != nil {
		role.ReadOnly = *body.ReadOnly
	}
	if body.CanSupport != nil {
		role.CanSupport = *body.CanSupport
	}
	database.DB.Save(&role)
	if _, ok := raw["allowed_modem_ids"]; ok {
		applyModemScope(&role, body.AllowedModemIDs)
	}
	OK(c, models.RoleOut(role))
}

// DeleteRole godoc
// @Summary 删除角色
// @Tags 角色管理
// @Produce json
// @Param id path int true "角色ID"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/roles/{id} [delete]
func DeleteRole(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var role models.Role
	if database.DB.First(&role, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "角色不存在")
		return
	}
	if role.IsSystem {
		Fail(c, http.StatusBadRequest, 400, "系统预置角色不可删除")
		return
	}
	database.DB.Delete(&role)
	OK(c, gin.H{"ok": true})
}

type setRolesBody struct {
	RoleIDs []uint `json:"role_ids"`
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
	var user models.User
	if database.DB.First(&user, id).Error != nil {
		Fail(c, http.StatusNotFound, 404, "用户不存在")
		return
	}
	var body setRolesBody
	c.ShouldBindJSON(&body)
	var roles []models.Role
	if len(body.RoleIDs) > 0 {
		database.DB.Where("id IN ?", body.RoleIDs).Find(&roles)
	}
	database.DB.Model(&user).Association("RbacRoles").Replace(roles)
	ids := make([]uint, 0, len(roles))
	for _, r := range roles {
		ids = append(ids, r.ID)
	}
	OK(c, gin.H{"ok": true, "user_id": id, "role_ids": ids})
}
