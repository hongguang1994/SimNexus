package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"simnexus-go/database"
	"simnexus-go/middleware"
	"simnexus-go/models"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// requestCreate 申请 SIM 卡访问权限的请求体。
type requestCreate struct {
	ModemID        uint   `json:"modem_id"`
	RequestedLevel string `json:"requested_level"` // view 或 use，默认 use
	Reason         string `json:"reason"`
}

// CreateSimRequest godoc
// @Summary 申请SIM卡访问权限
// @Tags SIM申请
// @Accept json
// @Produce json
// @Param body body requestCreate true "申请信息"
// @Success 200 {object} handlers.R{data=models.SimAccessRequest}
// @Security BearerAuth
// @Router /api/v1/sim-requests/ [post]
func CreateSimRequest(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var body requestCreate
	c.ShouldBindJSON(&body)
	svc := services.NewSimRequestService(database.DB)
	if err := svc.CreateRequest(me.ID, body.ModemID, body.RequestedLevel, body.Reason); err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidLevel):
			Fail(c, http.StatusBadRequest, 400, err.Error())
		case errors.Is(err, services.ErrDuplicateRequest):
			Fail(c, http.StatusBadRequest, 400, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "提交失败")
		}
		return
	}
	services.Push("sim_request", "新的SIM卡申请",
		"用户 "+me.Username+" 申请访问 SIM "+strconv.Itoa(int(body.ModemID)), "admin", nil)
	OK(c, gin.H{"ok": true})
}

// MyRequests godoc
// @Summary 获取我的申请记录
// @Tags SIM申请
// @Produce json
// @Success 200 {object} handlers.R{data=[]models.SimAccessRequest}
// @Security BearerAuth
// @Router /api/v1/sim-requests/my [get]
func MyRequests(c *gin.Context) {
	me := middleware.CurrentUser(c)
	svc := services.NewSimRequestService(database.DB)
	reqs, gm, err := svc.ListMyRequests(me.ID)
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(reqs))
	for i := range reqs {
		out = append(out, svc.FmtRequest(&reqs[i], gm))
	}
	OK(c, out)
}

// MyGrants godoc
// @Summary 获取我的已授权列表
// @Tags SIM申请
// @Produce json
// @Success 200 {object} handlers.R{data=[]models.SimAccessRequest}
// @Security BearerAuth
// @Router /api/v1/sim-requests/my-grants [get]
func MyGrants(c *gin.Context) {
	me := middleware.CurrentUser(c)
	svc := services.NewSimRequestService(database.DB)
	grants, err := svc.ListMyGrants(me.ID)
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	now := time.Now()
	out := make([]gin.H, 0)
	for _, g := range grants {
		// 跳过已过期的授权
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
	OK(c, out)
}

// ListRequests godoc
// @Summary 审批员查看申请列表
// @Tags SIM申请
// @Produce json
// @Param status query string false "状态过滤"
// @Success 200 {object} handlers.R{data=[]models.SimAccessRequest}
// @Security BearerAuth
// @Router /api/v1/sim-requests/ [get]
func ListRequests(c *gin.Context) {
	approver := middleware.CurrentUser(c)
	svc := services.NewSimRequestService(database.DB)
	scope := svc.GetApproverScope(approver)
	reqs, gm, err := svc.ListRequests(scope, c.Query("status"))
	if err != nil {
		Fail(c, http.StatusInternalServerError, 500, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(reqs))
	for i := range reqs {
		out = append(out, svc.FmtRequest(&reqs[i], gm))
	}
	OK(c, out)
}

// approveBody 批准申请的请求体。
type approveBody struct {
	GrantedLevel string     `json:"granted_level"` // view 或 use，默认 use
	ExpiresAt    *time.Time `json:"expires_at"`    // 有效期（nil 表示永久）
	AdminNote    string     `json:"admin_note"`    // 审批备注
}

// ApproveRequest godoc
// @Summary 批准申请
// @Tags SIM申请
// @Accept json
// @Produce json
// @Param id path int true "申请ID"
// @Param body body approveBody true "批准信息"
// @Success 200 {object} handlers.R
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
	svc := services.NewSimRequestService(database.DB)
	scope := svc.GetApproverScope(approver)
	if err := svc.ApproveRequest(approver.ID, scope, uint(id), body.GrantedLevel, body.ExpiresAt, body.AdminNote); err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidLevel):
			Fail(c, http.StatusBadRequest, 400, err.Error())
		case errors.Is(err, services.ErrSimRequestNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		case errors.Is(err, services.ErrSimRequestForbidden):
			Fail(c, http.StatusForbidden, 403, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "审批失败")
		}
		return
	}
	// 查询申请信息用于推送通知
	var req models.SimAccessRequest
	database.DB.First(&req, id)
	notifyApproved(req.UserID, req.ModemID, body.GrantedLevel, body.ExpiresAt, svc)
	OK(c, gin.H{"ok": true})
}

// rejectBody 拒绝申请的请求体。
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
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sim-requests/{id}/reject [put]
func RejectRequest(c *gin.Context) {
	approver := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var body rejectBody
	c.ShouldBindJSON(&body)
	svc := services.NewSimRequestService(database.DB)
	scope := svc.GetApproverScope(approver)
	req, err := svc.RejectRequest(scope, uint(id), body.AdminNote)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrSimRequestNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		case errors.Is(err, services.ErrSimRequestForbidden):
			Fail(c, http.StatusForbidden, 403, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "审批失败")
		}
		return
	}
	// 推送拒绝通知给申请用户
	msg := "你对 " + svc.ModemDisplayName(req.ModemID) + " 的申请未获批准"
	if body.AdminNote != "" {
		msg += "，原因：" + body.AdminNote
	}
	services.Push("sim_rejected", "SIM卡申请已拒绝", msg, "user", &req.UserID)
	OK(c, gin.H{"ok": true})
}

// batchApproveBody 批量审批通过的请求体。
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
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sim-requests/batch-approve [post]
func BatchApprove(c *gin.Context) {
	approver := middleware.CurrentUser(c)
	var body batchApproveBody
	c.ShouldBindJSON(&body)
	if body.GrantedLevel == "" {
		body.GrantedLevel = models.LevelUse
	}
	svc := services.NewSimRequestService(database.DB)
	scope := svc.GetApproverScope(approver)
	count, err := svc.BatchApprove(approver.ID, scope, body.IDs, body.GrantedLevel, body.ExpiresAt, body.AdminNote)
	if err != nil {
		Fail(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	OK(c, gin.H{"approved": count})
}

// directGrantBody 直接授权（无需申请）的请求体。
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
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sim-requests/grant [post]
func DirectGrant(c *gin.Context) {
	approver := middleware.CurrentUser(c)
	var body directGrantBody
	c.ShouldBindJSON(&body)
	if body.GrantedLevel == "" {
		body.GrantedLevel = models.LevelUse
	}
	svc := services.NewSimRequestService(database.DB)
	scope := svc.GetApproverScope(approver)
	if err := svc.DirectGrant(approver.ID, scope, body.UserID, body.ModemID, body.GrantedLevel, body.ExpiresAt); err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidLevel):
			Fail(c, http.StatusBadRequest, 400, err.Error())
		case errors.Is(err, services.ErrDirectGrantForbidden):
			Fail(c, http.StatusForbidden, 403, err.Error())
		case errors.Is(err, services.ErrModemNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "授权失败")
		}
		return
	}
	// 推送授权通知给被授权用户
	levelLabel := "使用权限"
	if body.GrantedLevel == models.LevelView {
		levelLabel = "查看权限"
	}
	services.Push("sim_approved", "SIM卡权限已授予",
		"管理员已授予你 "+svc.ModemDisplayName(body.ModemID)+" 的"+levelLabel, "user", &body.UserID)
	OK(c, gin.H{"ok": true})
}

// RevokeGrant godoc
// @Summary 撤销授权
// @Tags SIM申请
// @Produce json
// @Param id path int true "授权ID"
// @Success 200 {object} handlers.R
// @Security BearerAuth
// @Router /api/v1/sim-requests/grants/{id} [delete]
func RevokeGrant(c *gin.Context) {
	approver := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	svc := services.NewSimRequestService(database.DB)
	scope := svc.GetApproverScope(approver)
	grant, err := svc.RevokeGrant(scope, uint(id))
	if err != nil {
		switch {
		case errors.Is(err, services.ErrGrantNotFound):
			Fail(c, http.StatusNotFound, 404, err.Error())
		case errors.Is(err, services.ErrGrantForbidden):
			Fail(c, http.StatusForbidden, 403, err.Error())
		default:
			Fail(c, http.StatusInternalServerError, 500, "撤销失败")
		}
		return
	}
	uid := grant.UserID
	mid := grant.ModemID
	services.Push("sim_revoked", "SIM卡权限已撤销",
		"你对 SIM "+strconv.Itoa(int(mid))+" 的访问权限已被撤销", "user", &uid)
	OK(c, gin.H{"ok": true})
}

// notifyApproved 向用户推送申请批准通知。
func notifyApproved(userID, modemID uint, level string, expiresAt *time.Time, svc *services.SimRequestService) {
	levelLabel := "使用权限"
	if level == models.LevelView {
		levelLabel = "查看权限"
	}
	expStr := "（永久）"
	if expiresAt != nil {
		expStr = "，有效期至 " + expiresAt.Format("2006-01-02")
	}
	services.Push("sim_approved", "SIM卡申请已批准",
		"你对 "+svc.ModemDisplayName(modemID)+" 的申请已获批准"+levelLabel+expStr, "user", &userID)
}
