package handlers

import (
	"net/http"
	"strconv"
	"time"

	"simnexus-go/database"
	"simnexus-go/middleware"
	"simnexus-go/models"
	"simnexus-go/security"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// visibleModemIDs 返回当前用户可见的设备 ID 列表。
// unrestricted=true 表示管理员或无范围限制；permitted=false 表示无权限（应返回 403）。
func visibleModemIDs(u *models.User) ([]uint, bool, bool) {
	if u.IsAdmin() {
		return nil, true, true
	}
	p := security.Perm(u)
	if p == nil || !p.CanViewSim {
		return nil, false, false
	}
	granted := security.GetUserModemGrants(database.DB, u.ID, "", u)
	// intersect with restricted role scope (non-approver roles only)
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

// ListAvailableModems godoc
// @Summary 获取所有设备（资源库）
// @Tags 设备管理
// @Produce json
// @Success 200 {array} models.Modem
// @Security BearerAuth
// @Router /api/v1/modems/available [get]
func ListAvailableModems(c *gin.Context) {
	u := middleware.CurrentUser(c)
	if !u.IsAdmin() {
		p := security.Perm(u)
		if p == nil || !p.CanViewSim {
			c.JSON(http.StatusForbidden, gin.H{"detail": "无SIM卡查看权限"})
			return
		}
	}
	var modems []models.Modem
	database.DB.Where("is_active = ?", true).Order("id").Find(&modems)
	c.JSON(http.StatusOK, modems)
}

// ListModems godoc
// @Summary 获取用户有权限的设备列表
// @Tags 设备管理
// @Produce json
// @Success 200 {array} models.Modem
// @Security BearerAuth
// @Router /api/v1/modems/ [get]
func ListModems(c *gin.Context) {
	u := middleware.CurrentUser(c)
	if u.IsAdmin() {
		var modems []models.Modem
		database.DB.Where("is_active = ?", true).Order("id").Find(&modems)
		c.JSON(http.StatusOK, modems)
		return
	}
	ids, _, ok := visibleModemIDs(u)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"detail": "无SIM卡查看权限"})
		return
	}
	if len(ids) == 0 {
		c.JSON(http.StatusOK, []models.Modem{})
		return
	}
	var modems []models.Modem
	database.DB.Where("id IN ? AND is_active = ?", ids, true).Order("id").Find(&modems)
	c.JSON(http.StatusOK, modems)
}

// canAccessModem 检查用户是否有权访问指定设备（任意权限级别）。
func canAccessModem(u *models.User, modemID uint) bool {
	if u.IsAdmin() {
		return true
	}
	ids, _, ok := visibleModemIDs(u)
	if !ok {
		return false
	}
	return security.ContainsUint(ids, modemID)
}

// GetModem godoc
// @Summary 获取单个设备信息
// @Tags 设备管理
// @Produce json
// @Param id path int true "设备ID"
// @Success 200 {object} models.Modem
// @Security BearerAuth
// @Router /api/v1/modems/{id} [get]
func GetModem(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	u := middleware.CurrentUser(c)
	if !u.IsAdmin() && !canAccessModem(u, uint(id)) {
		c.JSON(http.StatusForbidden, gin.H{"detail": "无权访问该设备"})
		return
	}
	var modem models.Modem
	if database.DB.First(&modem, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "Modem not found"})
		return
	}
	c.JSON(http.StatusOK, modem)
}

// modemUpdate 调制解调器可编辑字段（目前仅支持修改别名）。
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
// @Success 200 {object} models.Modem
// @Security BearerAuth
// @Router /api/v1/modems/{id} [patch]
func UpdateModem(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var modem models.Modem
	if database.DB.First(&modem, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "Modem not found"})
		return
	}
	var data modemUpdate
	c.ShouldBindJSON(&data)
	if data.Alias != nil {
		modem.Alias = *data.Alias
	}
	database.DB.Save(&modem)
	c.JSON(http.StatusOK, modem)
}

// GetModemDetail godoc
// @Summary 获取设备详情（含短信统计）
// @Tags 设备管理
// @Produce json
// @Param id path int true "设备ID"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/modems/{id}/detail [get]
func GetModemDetail(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	u := middleware.CurrentUser(c)
	if !u.IsAdmin() && !canAccessModem(u, uint(id)) {
		c.JSON(http.StatusForbidden, gin.H{"detail": "无权访问该设备"})
		return
	}
	var modem models.Modem
	if database.DB.First(&modem, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "Modem not found"})
		return
	}
	var sent, received, today int64
	database.DB.Model(&models.SmsMessage{}).Where("modem_id = ? AND direction = ?", id, models.SmsOutbound).Count(&sent)
	database.DB.Model(&models.SmsMessage{}).Where("modem_id = ? AND direction = ?", id, models.SmsInbound).Count(&received)
	todayStart := time.Now().Truncate(24 * time.Hour)
	database.DB.Model(&models.SmsMessage{}).Where("modem_id = ? AND created_at >= ?", id, todayStart).Count(&today)

	out := gin.H{}
	remarshal(modem, &out)
	out["sms_sent"] = sent
	out["sms_received"] = received
	out["sms_today"] = today
	c.JSON(http.StatusOK, out)
}

// RefreshModem godoc
// @Summary 手动刷新设备状态
// @Tags 设备管理
// @Produce json
// @Param id path int true "设备ID"
// @Success 200 {object} models.Modem
// @Security BearerAuth
// @Router /api/v1/modems/{id}/refresh [post]
func RefreshModem(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var modem models.Modem
	if database.DB.First(&modem, id).Error != nil || modem.MmObjectPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"detail": "Modem not found"})
		return
	}
	info := services.GetModemInfo(modem.MmObjectPath)
	if info == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "Could not reach modem"})
		return
	}
	modem.SignalQuality = info.SignalQuality
	modem.Operator = info.Operator
	modem.Status = info.Status
	database.DB.Save(&modem)
	c.JSON(http.StatusOK, modem)
}
