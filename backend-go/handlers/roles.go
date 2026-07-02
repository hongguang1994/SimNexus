package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"simnexus-go/database"
	"simnexus-go/models"
	"simnexus-go/services"

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
	AllowedModemIDs    *[]uint `json:"allowed_modem_ids"`
}

// ListRoles godoc
// @Summary 获取角色列表
// @Tags 角色管理
// @Produce json
// @Success 200 {object} handlers.R{data=[]models.Role}
// @Security BearerAuth
// @Router /api/v1/roles/ [get]
func ListRoles(c *gin.Context) {
	svc := services.NewRoleService(database.DB)
	roles, err := svc.ListRoles()
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(roles))
	for _, r := range roles {
		out = append(out, models.RoleOut(r))
	}
	OK(c, out)
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
	// 先解析为 raw map，用于检测字段是否存在（区分"未传"与"传空"）
	var raw map[string]interface{}
	c.ShouldBindJSON(&raw)
	var body roleBody
	remarshal(raw, &body)

	svc := services.NewRoleService(database.DB)
	role, err := svc.CreateRole(services.RoleCreateInput{
		Name:               body.Name,
		Description:        body.Description,
		CanViewSim:         body.CanViewSim,
		CanApproveRequests: body.CanApproveRequests,
		CanViewHistory:     body.CanViewHistory,
		ReadOnly:           body.ReadOnly,
		CanSupport:         body.CanSupport,
		AllowedModemIDs:    body.AllowedModemIDs,
	})
	if err != nil {
		if errors.Is(err, services.ErrRoleExists) {
			Fail(c, http.StatusBadRequest, 400, err.Error())
		} else {
			Fail(c, http.StatusInternalServerError, 500, "创建失败")
		}
		return
	}
	OK(c, models.RoleOut(*role))
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
	var raw map[string]interface{}
	c.ShouldBindJSON(&raw)
	var body roleBody
	remarshal(raw, &body)

	// 通过检测原始 map 的 key 判断请求体是否包含 allowed_modem_ids
	_, hasScopeField := raw["allowed_modem_ids"]

	var descPtr *string
	if _, ok := raw["description"]; ok {
		descPtr = &body.Description
	}

	svc := services.NewRoleService(database.DB)
	role, err := svc.UpdateRole(uint(id), services.RoleUpdateInput{
		Name:               body.Name,
		Description:        descPtr,
		CanViewSim:         body.CanViewSim,
		CanApproveRequests: body.CanApproveRequests,
		CanViewHistory:     body.CanViewHistory,
		ReadOnly:           body.ReadOnly,
		CanSupport:         body.CanSupport,
		AllowedModemIDs:    body.AllowedModemIDs,
		HasScopeField:      hasScopeField,
	})
	if err != nil {
		if errors.Is(err, services.ErrRoleNotFound) {
			Fail(c, http.StatusNotFound, 404, err.Error())
		} else {
			Fail(c, http.StatusInternalServerError, 500, "更新失败")
		}
		return
	}
	OK(c, models.RoleOut(*role))
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
	svc := services.NewRoleService(database.DB)
	if err := svc.DeleteRole(uint(id)); err != nil {
		switch {
		case errors.Is(err, services.ErrRoleNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		case errors.Is(err, services.ErrSystemRole):
			Fail(c, http.StatusBadRequest, 400, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "删除失败")
		}
		return
	}
	OK(c, gin.H{"ok": true})
}
