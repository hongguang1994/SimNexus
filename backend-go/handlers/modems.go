package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"simnexus-go/database"
	"simnexus-go/middleware"
	"simnexus-go/security"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// ListAvailableModems godoc
// @Summary 获取所有设备（资源库）
// @Tags 设备管理
// @Produce json
// @Success 200 {object} handlers.R{data=[]models.Modem}
// @Security BearerAuth
// @Router /api/v1/modems/available [get]
func ListAvailableModems(c *gin.Context) {
	u := middleware.CurrentUser(c)
	// 非管理员需要有 can_view_sim 权限才能访问资源库
	if !u.IsAdmin() {
		p := security.Perm(u)
		if p == nil || !p.CanViewSim {
			Fail(c, http.StatusForbidden, 403, "无SIM卡查看权限")
			return
		}
	}
	svc := services.NewModemService(database.DB)
	modems, err := svc.ListAvailableModems()
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	OK(c, modems)
}

// ListModems godoc
// @Summary 获取用户有权限的设备列表
// @Tags 设备管理
// @Produce json
// @Success 200 {object} handlers.R{data=[]models.Modem}
// @Security BearerAuth
// @Router /api/v1/modems/ [get]
func ListModems(c *gin.Context) {
	u := middleware.CurrentUser(c)
	svc := services.NewModemService(database.DB)
	modems, err := svc.ListUserModems(u)
	if err != nil {
		if errors.Is(err, services.ErrNoViewPerm) {
			Fail(c, http.StatusForbidden, 403, err.Error())
		} else {
			Fail(c, http.StatusInternalServerError, 500, "查询失败")
		}
		return
	}
	OK(c, modems)
}

// GetModem godoc
// @Summary 获取单个设备信息
// @Tags 设备管理
// @Produce json
// @Param id path int true "设备ID"
// @Success 200 {object} handlers.R{data=models.Modem}
// @Security BearerAuth
// @Router /api/v1/modems/{id} [get]
func GetModem(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	u := middleware.CurrentUser(c)
	svc := services.NewModemService(database.DB)
	modem, err := svc.GetModem(uint(id), u)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrModemForbidden):
			Fail(c, http.StatusForbidden, 403, err.Error())
		case errors.Is(err, services.ErrModemNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "查询失败")
		}
		return
	}
	OK(c, modem)
}

// modemUpdate 设备可编辑字段（目前仅支持别名）。
type modemUpdate struct {
	Alias *string `json:"alias"`
}

// UpdateModem godoc
// @Summary 修改设备属性
// @Tags 设备管理
// @Accept json
// @Produce json
// @Param id path int true "设备ID"
// @Param body body modemUpdate true "修改字段"
// @Success 200 {object} handlers.R{data=models.Modem}
// @Security BearerAuth
// @Router /api/v1/modems/{id} [patch]
func UpdateModem(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var data modemUpdate
	c.ShouldBindJSON(&data)
	svc := services.NewModemService(database.DB)
	modem, err := svc.UpdateModemAlias(uint(id), data.Alias)
	if err != nil {
		Fail(c, http.StatusNotFound, 404, err.Error())
		return
	}
	OK(c, modem)
}

// GetModemDetail godoc
// @Summary 获取设备详情（含短信统计）
// @Tags 设备管理
// @Produce json
// @Param id path int true "设备ID"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/modems/{id}/detail [get]
func GetModemDetail(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	u := middleware.CurrentUser(c)
	svc := services.NewModemService(database.DB)
	detail, err := svc.GetModemDetail(uint(id), u)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrModemForbidden):
			Fail(c, http.StatusForbidden, 403, err.Error())
		case errors.Is(err, services.ErrModemNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "查询失败")
		}
		return
	}
	OK(c, detail)
}

// RefreshModem godoc
// @Summary 手动刷新设备状态
// @Tags 设备管理
// @Produce json
// @Param id path int true "设备ID"
// @Success 200 {object} handlers.R{data=models.Modem}
// @Security BearerAuth
// @Router /api/v1/modems/{id}/refresh [post]
func RefreshModem(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	svc := services.NewModemService(database.DB)
	modem, err := svc.RefreshModem(uint(id))
	if err != nil {
		switch {
		case errors.Is(err, services.ErrModemNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		default:
			Fail(c, http.StatusServiceUnavailable, 503, err.Error())
		}
		return
	}
	OK(c, modem)
}
