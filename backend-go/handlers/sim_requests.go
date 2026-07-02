package handlers

import (
	"net/http"
	"strconv"
	"time"

	"simnexus-go/database"
	"simnexus-go/middleware"
	"simnexus-go/models"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// approverModemScope 返回审批员可管理的设备 ID 集合。
// unrestricted=true 表示无限制（管理员或 ModemScope 为空的审批角色）。
func approverModemScope(u *models.User) ([]uint, bool) {
	if u.IsAdmin() {
		return nil, true
	}
	var approverRoles []models.Role
	for _, r := range u.RbacRoles {
		if r.CanApproveRequests {
			approverRoles = append(approverRoles, r)
		}
	}
	if len(approverRoles) == 0 {
		return []uint{}, false
	}
	set := map[uint]struct{}{}
	for _, r := range approverRoles {
		if len(r.ModemScope) == 0 {
			return nil, true
		}
		for _, m := range r.ModemScope {
			set[m.ID] = struct{}{}
		}
	}
	ids := make([]uint, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids, false
}

// inScope 判断给定设备是否在审批员的管辖范围内。
func inScope(ids []uint, unrestricted bool, modemID uint) bool {
	if unrestricted {
		return true
	}
	for _, id := range ids {
		if id == modemID {
			return true
		}
	}
	return false
}

// fmtRequest 将申请记录格式化为 API 响应，包含用户名、设备名、授权状态和是否过期。
func fmtRequest(r *models.SimAccessRequest, grants map[[2]uint]*models.SimGrant) gin.H {
	now := time.Now()
	var username, modemName string
	var user models.User
	if database.DB.First(&user, r.UserID).Error == nil {
		username = user.Username
	}
	var modem models.Modem
	if database.DB.First(&modem, r.ModemID).Error == nil && modem.Alias != "" {
		modemName = modem.Alias
	} else {
		modemName = "SIM " + strconv.Itoa(int(r.ModemID))
	}
	g := grants[[2]uint{r.UserID, r.ModemID}]
	var grantedLevel interface{}
	var expiresAt interface{}
	isExpired := false
	if g != nil {
		grantedLevel = g.GrantedLevel
		if g.ExpiresAt != nil {
			expiresAt = g.ExpiresAt.Format(time.RFC3339)
			isExpired = g.ExpiresAt.Before(now)
		}
	}
	return gin.H{
		"id":              r.ID,
		"user_id":         r.UserID,
		"username":        username,
		"modem_id":        r.ModemID,
		"modem_name":      modemName,
		"status":          r.Status,
		"requested_level": r.RequestedLevel,
		"granted_level":   grantedLevel,
		"reason":          r.Reason,
		"admin_note":      r.AdminNote,
		"expires_at":      expiresAt,
		"created_at":      r.CreatedAt,
		"updated_at":      r.UpdatedAt,
		"is_expired":      isExpired,
	}
}

// upsertGrant 创建或更新 (userID, modemID) 的授权记录，已有记录则原地更新。
func upsertGrant(userID, modemID uint, level string, expiresAt *time.Time, grantedByID uint, requestID *uint) {
	now := time.Now()
	var existing models.SimGrant
	err := database.DB.Where("user_id = ? AND modem_id = ?", userID, modemID).First(&existing).Error
	if err == nil {
		existing.GrantedLevel = level
		existing.ExpiresAt = expiresAt
		existing.GrantedByID = &grantedByID
		if requestID != nil {
			existing.RequestID = requestID
		}
		existing.UpdatedAt = now
		database.DB.Save(&existing)
	} else {
		database.DB.Create(&models.SimGrant{
			UserID: userID, ModemID: modemID, GrantedLevel: level,
			ExpiresAt: expiresAt, GrantedByID: &grantedByID, RequestID: requestID,
			CreatedAt: now, UpdatedAt: now,
		})
	}
}

// modemName 返回设备的展示名称（别名优先，否则为 "SIM <id>"）。
func modemName(modemID uint) string {
	var modem models.Modem
	if database.DB.First(&modem, modemID).Error == nil && modem.Alias != "" {
		return modem.Alias
	}
	return "SIM " + strconv.Itoa(int(modemID))
}

type requestCreate struct {
	ModemID        uint   `json:"modem_id"`
	RequestedLevel string `json:"requested_level"`
	Reason         string `json:"reason"`
}

// CreateSimRequest godoc
// @Summary 申请SIM卡访问权限
// @Tags SIM申请
// @Accept json
// @Produce json
// @Param body body requestCreate true "申请信息"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sim-requests/ [post]
func CreateSimRequest(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var body requestCreate
	c.ShouldBindJSON(&body)
	if body.RequestedLevel == "" {
		body.RequestedLevel = models.LevelUse
	}
	if body.RequestedLevel != models.LevelView && body.RequestedLevel != models.LevelUse {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "requested_level 必须是 view 或 use"})
		return
	}
	now := time.Now()
	var eg models.SimGrant
	if database.DB.Where("user_id = ? AND modem_id = ?", me.ID, body.ModemID).First(&eg).Error == nil {
		if eg.ExpiresAt == nil || eg.ExpiresAt.After(now) {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "已有有效授权，无需重复申请"})
			return
		}
	}
	var pending models.SimAccessRequest
	if database.DB.Where("user_id = ? AND modem_id = ? AND status = ?", me.ID, body.ModemID, models.ReqPending).First(&pending).Error == nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "已有待审批的申请，请勿重复提交"})
		return
	}
	var reason *string
	if body.Reason != "" {
		reason = &body.Reason
	}
	req := models.SimAccessRequest{
		UserID: me.ID, ModemID: body.ModemID,
		RequestedLevel: body.RequestedLevel, Reason: reason, Status: models.ReqPending,
	}
	database.DB.Create(&req)
	services.Push("sim_request", "新的SIM卡申请",
		"用户 "+me.Username+" 申请访问 SIM "+strconv.Itoa(int(body.ModemID)), "admin", nil)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// MyRequests godoc
// @Summary 获取我的申请记录
// @Tags SIM申请
// @Produce json
// @Success 200 {array} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sim-requests/my [get]
func MyRequests(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var reqs []models.SimAccessRequest
	database.DB.Where("user_id = ?", me.ID).Order("created_at desc").Find(&reqs)
	var grants []models.SimGrant
	database.DB.Where("user_id = ?", me.ID).Find(&grants)
	gm := map[[2]uint]*models.SimGrant{}
	for i := range grants {
		gm[[2]uint{grants[i].UserID, grants[i].ModemID}] = &grants[i]
	}
	out := make([]gin.H, 0, len(reqs))
	for i := range reqs {
		out = append(out, fmtRequest(&reqs[i], gm))
	}
	c.JSON(http.StatusOK, out)
}

// MyGrants godoc
// @Summary 获取我的已授权列表
// @Tags SIM申请
// @Produce json
// @Success 200 {array} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sim-requests/my-grants [get]
func MyGrants(c *gin.Context) {
	me := middleware.CurrentUser(c)
	now := time.Now()
	var grants []models.SimGrant
	database.DB.Where("user_id = ?", me.ID).Find(&grants)
	out := make([]gin.H, 0)
	for _, g := range grants {
		if g.ExpiresAt != nil && g.ExpiresAt.Before(now) {
			continue
		}
		var exp interface{}
		if g.ExpiresAt != nil {
			exp = g.ExpiresAt.Format(time.RFC3339)
		}
		out = append(out, gin.H{
			"id": g.ID, "user_id": g.UserID, "modem_id": g.ModemID,
			"granted_level": g.GrantedLevel, "expires_at": exp,
			"is_expired": false, "created_at": g.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, out)
}

// ListRequests godoc
// @Summary 审批员查看申请列表
// @Tags SIM申请
// @Produce json
// @Param status query string false "状态过滤"
// @Success 200 {array} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sim-requests/ [get]
func ListRequests(c *gin.Context) {
	approver := middleware.CurrentUser(c)
	ids, unrestricted := approverModemScope(approver)
	q := database.DB.Model(&models.SimAccessRequest{})
	if !unrestricted {
		q = q.Where("modem_id IN ?", ids)
	}
	if st := c.Query("status"); st != "" {
		q = q.Where("status = ?", st)
	}
	var reqs []models.SimAccessRequest
	q.Order("created_at desc").Find(&reqs)

	gm := map[[2]uint]*models.SimGrant{}
	if len(reqs) > 0 {
		userIDs := map[uint]bool{}
		modemIDs := map[uint]bool{}
		for _, r := range reqs {
			userIDs[r.UserID] = true
			modemIDs[r.ModemID] = true
		}
		var grants []models.SimGrant
		database.DB.Where("user_id IN ? AND modem_id IN ?", keys(userIDs), keys(modemIDs)).Find(&grants)
		for i := range grants {
			gm[[2]uint{grants[i].UserID, grants[i].ModemID}] = &grants[i]
		}
	}
	out := make([]gin.H, 0, len(reqs))
	for i := range reqs {
		out = append(out, fmtRequest(&reqs[i], gm))
	}
	c.JSON(http.StatusOK, out)
}

type approveBody struct {
	GrantedLevel string     `json:"granted_level"`
	ExpiresAt    *time.Time `json:"expires_at"`
	AdminNote    string     `json:"admin_note"`
}

// ApproveRequest godoc
// @Summary 批准申请
// @Tags SIM申请
// @Accept json
// @Produce json
// @Param id path int true "申请ID"
// @Param body body approveBody true "批准信息"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sim-requests/{id}/approve [put]
func ApproveRequest(c *gin.Context) {
	approver := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var body approveBody
	c.ShouldBindJSON(&body)
	if body.GrantedLevel == "" {
		body.GrantedLevel = models.LevelUse
	}
	if body.GrantedLevel != models.LevelView && body.GrantedLevel != models.LevelUse {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "granted_level 必须是 view 或 use"})
		return
	}
	var req models.SimAccessRequest
	if database.DB.First(&req, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "申请不存在"})
		return
	}
	ids, unrestricted := approverModemScope(approver)
	if !inScope(ids, unrestricted, req.ModemID) {
		c.JSON(http.StatusForbidden, gin.H{"detail": "无权审批该设备的申请"})
		return
	}
	req.Status = models.ReqApproved
	setNote(&req, body.AdminNote)
	req.UpdatedAt = time.Now()
	database.DB.Save(&req)
	upsertGrant(req.UserID, req.ModemID, body.GrantedLevel, body.ExpiresAt, approver.ID, &req.ID)
	notifyApproved(req.UserID, req.ModemID, body.GrantedLevel, body.ExpiresAt)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type rejectBody struct {
	AdminNote string `json:"admin_note"`
}

// RejectRequest godoc
// @Summary 拒绝申请
// @Tags SIM申请
// @Accept json
// @Produce json
// @Param id path int true "申请ID"
// @Param body body rejectBody true "拒绝原因"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sim-requests/{id}/reject [put]
func RejectRequest(c *gin.Context) {
	approver := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var body rejectBody
	c.ShouldBindJSON(&body)
	var req models.SimAccessRequest
	if database.DB.First(&req, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "申请不存在"})
		return
	}
	ids, unrestricted := approverModemScope(approver)
	if !inScope(ids, unrestricted, req.ModemID) {
		c.JSON(http.StatusForbidden, gin.H{"detail": "无权审批该设备的申请"})
		return
	}
	req.Status = models.ReqRejected
	setNote(&req, body.AdminNote)
	req.UpdatedAt = time.Now()
	database.DB.Save(&req)
	body2 := "你对 " + modemName(req.ModemID) + " 的申请未获批准"
	if body.AdminNote != "" {
		body2 += "，原因：" + body.AdminNote
	}
	services.Push("sim_rejected", "SIM卡申请已拒绝", body2, "user", &req.UserID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type batchApproveBody struct {
	IDs          []uint     `json:"ids"`
	GrantedLevel string     `json:"granted_level"`
	ExpiresAt    *time.Time `json:"expires_at"`
	AdminNote    string     `json:"admin_note"`
}

// BatchApprove godoc
// @Summary 批量审批通过
// @Tags SIM申请
// @Accept json
// @Produce json
// @Param body body batchApproveBody true "批量审批参数"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sim-requests/batch-approve [post]
func BatchApprove(c *gin.Context) {
	approver := middleware.CurrentUser(c)
	var body batchApproveBody
	c.ShouldBindJSON(&body)
	if body.GrantedLevel == "" {
		body.GrantedLevel = models.LevelUse
	}
	if body.GrantedLevel != models.LevelView && body.GrantedLevel != models.LevelUse {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "granted_level 必须是 view 或 use"})
		return
	}
	ids, unrestricted := approverModemScope(approver)
	var reqs []models.SimAccessRequest
	database.DB.Where("id IN ?", body.IDs).Find(&reqs)
	count := 0
	for i := range reqs {
		req := &reqs[i]
		if !inScope(ids, unrestricted, req.ModemID) {
			continue
		}
		req.Status = models.ReqApproved
		setNote(req, body.AdminNote)
		req.UpdatedAt = time.Now()
		database.DB.Save(req)
		upsertGrant(req.UserID, req.ModemID, body.GrantedLevel, body.ExpiresAt, approver.ID, &req.ID)
		notifyApproved(req.UserID, req.ModemID, body.GrantedLevel, body.ExpiresAt)
		count++
	}
	c.JSON(http.StatusOK, gin.H{"approved": count})
}

type directGrantBody struct {
	UserID       uint       `json:"user_id"`
	ModemID      uint       `json:"modem_id"`
	GrantedLevel string     `json:"granted_level"`
	ExpiresAt    *time.Time `json:"expires_at"`
	AdminNote    string     `json:"admin_note"`
}

// DirectGrant godoc
// @Summary 直接授权（无需申请）
// @Tags SIM申请
// @Accept json
// @Produce json
// @Param body body directGrantBody true "授权信息"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sim-requests/grant [post]
func DirectGrant(c *gin.Context) {
	approver := middleware.CurrentUser(c)
	var body directGrantBody
	c.ShouldBindJSON(&body)
	if body.GrantedLevel == "" {
		body.GrantedLevel = models.LevelUse
	}
	if body.GrantedLevel != models.LevelView && body.GrantedLevel != models.LevelUse {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "granted_level 必须是 view 或 use"})
		return
	}
	ids, unrestricted := approverModemScope(approver)
	if !inScope(ids, unrestricted, body.ModemID) {
		c.JSON(http.StatusForbidden, gin.H{"detail": "无权授权该设备"})
		return
	}
	var modem models.Modem
	if database.DB.First(&modem, body.ModemID).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "设备不存在"})
		return
	}
	upsertGrant(body.UserID, body.ModemID, body.GrantedLevel, body.ExpiresAt, approver.ID, nil)
	label := "使用权限"
	if body.GrantedLevel == models.LevelView {
		label = "查看权限"
	}
	services.Push("sim_approved", "SIM卡权限已授予",
		"管理员已授予你 "+modemName(body.ModemID)+" 的"+label, "user", &body.UserID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// RevokeGrant godoc
// @Summary 撤销授权
// @Tags SIM申请
// @Produce json
// @Param id path int true "授权ID"
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/sim-requests/grants/{id} [delete]
func RevokeGrant(c *gin.Context) {
	approver := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var grant models.SimGrant
	if database.DB.First(&grant, id).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "授权记录不存在"})
		return
	}
	ids, unrestricted := approverModemScope(approver)
	if !inScope(ids, unrestricted, grant.ModemID) {
		c.JSON(http.StatusForbidden, gin.H{"detail": "无权撤销该设备的授权"})
		return
	}
	uid := grant.UserID
	mid := grant.ModemID
	database.DB.Delete(&grant)
	services.Push("sim_revoked", "SIM卡权限已撤销",
		"你对 SIM "+strconv.Itoa(int(mid))+" 的访问权限已被撤销", "user", &uid)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// setNote 将审批备注写入申请记录，空字符串时置 nil。
func setNote(r *models.SimAccessRequest, note string) {
	if note == "" {
		r.AdminNote = nil
	} else {
		r.AdminNote = &note
	}
}

// notifyApproved 向用户推送申请批准通知，包含权限级别和有效期信息。
func notifyApproved(userID, modemID uint, level string, expiresAt *time.Time) {
	levelLabel := "使用权限"
	if level == models.LevelView {
		levelLabel = "查看权限"
	}
	expStr := "（永久）"
	if expiresAt != nil {
		expStr = "，有效期至 " + expiresAt.Format("2006-01-02")
	}
	services.Push("sim_approved", "SIM卡申请已批准",
		"你对 "+modemName(modemID)+" 的申请已获批准"+levelLabel+expStr, "user", &userID)
}

// keys 将 map[uint]bool 的所有键提取为 slice。
func keys(m map[uint]bool) []uint {
	out := make([]uint, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
