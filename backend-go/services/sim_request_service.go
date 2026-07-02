package services

import (
	"errors"
	"strconv"
	"time"

	"simnexus-go/models"

	"gorm.io/gorm"
)

// ErrSimRequestNotFound SIM 申请记录不存在。
var ErrSimRequestNotFound = errors.New("申请不存在")

// ErrGrantNotFound 授权记录不存在。
var ErrGrantNotFound = errors.New("授权记录不存在")

// ErrSimRequestForbidden 无权操作该设备的申请。
var ErrSimRequestForbidden = errors.New("无权审批该设备的申请")

// ErrGrantForbidden 无权撤销该设备的授权。
var ErrGrantForbidden = errors.New("无权撤销该设备的授权")

// ErrDirectGrantForbidden 无权给该设备直接授权。
var ErrDirectGrantForbidden = errors.New("无权授权该设备")

// ErrDuplicateRequest 已有有效授权或待审批申请，无需重复提交。
var ErrDuplicateRequest = errors.New("已有有效授权或待审批的申请，无需重复提交")

// ErrInvalidLevel granted_level 或 requested_level 值非法。
var ErrInvalidLevel = errors.New("权限级别必须是 view 或 use")

// SimRequestService 提供 SIM 卡申请、审批、授权相关的数据库操作。
type SimRequestService struct {
	db *gorm.DB
}

// NewSimRequestService 创建 SimRequestService 实例。
func NewSimRequestService(db *gorm.DB) *SimRequestService {
	return &SimRequestService{db: db}
}

// ApproverScope 审批员的设备管辖范围。
type ApproverScope struct {
	IDs          []uint // 可管辖的设备 ID 列表（Unrestricted=true 时忽略）
	Unrestricted bool   // true 表示无限制（管理员或角色无 ModemScope）
}

// GetApproverScope 计算指定用户作为审批员的设备管辖范围。
// 管理员无限制；审批角色未设置 ModemScope 则也无限制。
func (s *SimRequestService) GetApproverScope(u *models.User) ApproverScope {
	if u.IsAdmin() {
		return ApproverScope{Unrestricted: true}
	}
	// 收集所有拥有审批权限的角色
	var approverRoles []models.Role
	for _, r := range u.RbacRoles {
		if r.CanApproveRequests {
			approverRoles = append(approverRoles, r)
		}
	}
	if len(approverRoles) == 0 {
		return ApproverScope{IDs: []uint{}}
	}
	set := map[uint]struct{}{}
	for _, r := range approverRoles {
		if len(r.ModemScope) == 0 {
			// 任意角色没有设备范围限制 → 整个审批员无限制
			return ApproverScope{Unrestricted: true}
		}
		for _, m := range r.ModemScope {
			set[m.ID] = struct{}{}
		}
	}
	ids := make([]uint, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ApproverScope{IDs: ids}
}

// InScope 判断指定设备是否在管辖范围内。
func (sc ApproverScope) InScope(modemID uint) bool {
	if sc.Unrestricted {
		return true
	}
	for _, id := range sc.IDs {
		if id == modemID {
			return true
		}
	}
	return false
}

// CreateRequest 用户提交 SIM 卡访问申请。
// 校验：权限级别合法、无已有有效授权、无已有待审批申请。
func (s *SimRequestService) CreateRequest(userID, modemID uint, requestedLevel string, reason string) error {
	if requestedLevel == "" {
		requestedLevel = models.LevelUse
	}
	if !validLevel(requestedLevel) {
		return ErrInvalidLevel
	}
	now := time.Now()
	// 检查是否已有有效授权（未过期）
	var existing models.SimGrant
	if s.db.Where("user_id = ? AND modem_id = ?", userID, modemID).First(&existing).Error == nil {
		if existing.ExpiresAt == nil || existing.ExpiresAt.After(now) {
			return ErrDuplicateRequest
		}
	}
	// 检查是否已有待审批中的申请
	var pending models.SimAccessRequest
	if s.db.Where("user_id = ? AND modem_id = ? AND status = ?",
		userID, modemID, models.ReqPending).First(&pending).Error == nil {
		return ErrDuplicateRequest
	}
	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}
	req := models.SimAccessRequest{
		UserID: userID, ModemID: modemID,
		RequestedLevel: requestedLevel, Reason: reasonPtr, Status: models.ReqPending,
	}
	return s.db.Create(&req).Error
}

// ListMyRequests 查询用户自己的申请记录，附带授权状态。
func (s *SimRequestService) ListMyRequests(userID uint) ([]models.SimAccessRequest, map[[2]uint]*models.SimGrant, error) {
	var reqs []models.SimAccessRequest
	if err := s.db.Where("user_id = ?", userID).Order("created_at desc").Find(&reqs).Error; err != nil {
		return nil, nil, err
	}
	gm, err := s.loadGrantMap([]uint{userID}, nil)
	return reqs, gm, err
}

// ListMyGrants 查询用户当前所有有效（未过期）的授权记录。
func (s *SimRequestService) ListMyGrants(userID uint) ([]models.SimGrant, error) {
	var grants []models.SimGrant
	err := s.db.Where("user_id = ?", userID).Find(&grants).Error
	return grants, err
}

// ListRequests 审批员查看申请列表（按自身管辖范围过滤），可按状态筛选。
func (s *SimRequestService) ListRequests(scope ApproverScope, status string) ([]models.SimAccessRequest, map[[2]uint]*models.SimGrant, error) {
	q := s.db.Model(&models.SimAccessRequest{})
	if !scope.Unrestricted {
		q = q.Where("modem_id IN ?", scope.IDs)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var reqs []models.SimAccessRequest
	if err := q.Order("created_at desc").Find(&reqs).Error; err != nil {
		return nil, nil, err
	}
	// 批量预加载授权记录，避免 N+1 查询
	userIDs := map[uint]bool{}
	modemIDs := map[uint]bool{}
	for _, r := range reqs {
		userIDs[r.UserID] = true
		modemIDs[r.ModemID] = true
	}
	gm, err := s.loadGrantMap(mapKeys(userIDs), mapKeys(modemIDs))
	return reqs, gm, err
}

// ApproveRequest 批准单条申请，同时创建/更新授权记录。
func (s *SimRequestService) ApproveRequest(approverID uint, scope ApproverScope, reqID uint, grantedLevel string, expiresAt *time.Time, note string) error {
	if !validLevel(grantedLevel) {
		return ErrInvalidLevel
	}
	var req models.SimAccessRequest
	if s.db.First(&req, reqID).Error != nil {
		return ErrSimRequestNotFound
	}
	if !scope.InScope(req.ModemID) {
		return ErrSimRequestForbidden
	}
	req.Status = models.ReqApproved
	setNoteField(&req, note)
	req.UpdatedAt = time.Now()
	s.db.Save(&req)
	s.upsertGrant(req.UserID, req.ModemID, grantedLevel, expiresAt, approverID, &req.ID)
	return nil
}

// RejectRequest 拒绝单条申请。
func (s *SimRequestService) RejectRequest(scope ApproverScope, reqID uint, note string) (*models.SimAccessRequest, error) {
	var req models.SimAccessRequest
	if s.db.First(&req, reqID).Error != nil {
		return nil, ErrSimRequestNotFound
	}
	if !scope.InScope(req.ModemID) {
		return nil, ErrSimRequestForbidden
	}
	req.Status = models.ReqRejected
	setNoteField(&req, note)
	req.UpdatedAt = time.Now()
	s.db.Save(&req)
	return &req, nil
}

// BatchApprove 批量批准申请，跳过不在管辖范围内的记录。返回实际批准数量。
func (s *SimRequestService) BatchApprove(approverID uint, scope ApproverScope, reqIDs []uint, grantedLevel string, expiresAt *time.Time, note string) (int, error) {
	if !validLevel(grantedLevel) {
		return 0, ErrInvalidLevel
	}
	var reqs []models.SimAccessRequest
	s.db.Where("id IN ?", reqIDs).Find(&reqs)
	count := 0
	for i := range reqs {
		req := &reqs[i]
		if !scope.InScope(req.ModemID) {
			// 不在管辖范围内的申请静默跳过
			continue
		}
		req.Status = models.ReqApproved
		setNoteField(req, note)
		req.UpdatedAt = time.Now()
		s.db.Save(req)
		s.upsertGrant(req.UserID, req.ModemID, grantedLevel, expiresAt, approverID, &req.ID)
		count++
	}
	return count, nil
}

// DirectGrant 审批员直接给用户授权，无需提交申请。
// 校验：设备必须在审批员管辖范围内，且设备存在。
func (s *SimRequestService) DirectGrant(approverID uint, scope ApproverScope, userID, modemID uint, grantedLevel string, expiresAt *time.Time) error {
	if !validLevel(grantedLevel) {
		return ErrInvalidLevel
	}
	if !scope.InScope(modemID) {
		return ErrDirectGrantForbidden
	}
	var modem models.Modem
	if s.db.First(&modem, modemID).Error != nil {
		return ErrModemNotFound
	}
	s.upsertGrant(userID, modemID, grantedLevel, expiresAt, approverID, nil)
	return nil
}

// RevokeGrant 撤销授权记录，需在审批员管辖范围内。
func (s *SimRequestService) RevokeGrant(scope ApproverScope, grantID uint) (*models.SimGrant, error) {
	var grant models.SimGrant
	if s.db.First(&grant, grantID).Error != nil {
		return nil, ErrGrantNotFound
	}
	if !scope.InScope(grant.ModemID) {
		return nil, ErrGrantForbidden
	}
	s.db.Delete(&grant)
	return &grant, nil
}

// ModemDisplayName 返回设备展示名称（别名优先，否则 "SIM <id>"）。
func (s *SimRequestService) ModemDisplayName(modemID uint) string {
	var modem models.Modem
	if s.db.First(&modem, modemID).Error == nil && modem.Alias != "" {
		return modem.Alias
	}
	return "SIM " + strconv.Itoa(int(modemID))
}

// FmtRequest 将申请记录格式化为 API 响应，包含用户名、设备名、授权状态和是否过期。
func (s *SimRequestService) FmtRequest(r *models.SimAccessRequest, grants map[[2]uint]*models.SimGrant) map[string]interface{} {
	now := time.Now()
	var username, modemName string
	var user models.User
	if s.db.First(&user, r.UserID).Error == nil {
		username = user.Username
	}
	var modem models.Modem
	if s.db.First(&modem, r.ModemID).Error == nil && modem.Alias != "" {
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
	return map[string]interface{}{
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

// upsertGrant 创建或更新 (userID, modemID) 的授权记录，保证幂等。
// requestID 可为 nil（直接授权时无关联申请）。
func (s *SimRequestService) upsertGrant(userID, modemID uint, level string, expiresAt *time.Time, grantedByID uint, requestID *uint) {
	now := time.Now()
	var existing models.SimGrant
	if s.db.Where("user_id = ? AND modem_id = ?", userID, modemID).First(&existing).Error == nil {
		// 记录已存在：原地更新，避免产生重复行
		existing.GrantedLevel = level
		existing.ExpiresAt = expiresAt
		existing.GrantedByID = &grantedByID
		if requestID != nil {
			existing.RequestID = requestID
		}
		existing.UpdatedAt = now
		s.db.Save(&existing)
	} else {
		s.db.Create(&models.SimGrant{
			UserID: userID, ModemID: modemID, GrantedLevel: level,
			ExpiresAt: expiresAt, GrantedByID: &grantedByID, RequestID: requestID,
			CreatedAt: now, UpdatedAt: now,
		})
	}
}

// loadGrantMap 批量加载授权记录并索引为 (userID, modemID) → *SimGrant。
// modemIDs=nil 表示不限制设备范围（只按 userID 过滤）。
func (s *SimRequestService) loadGrantMap(userIDs, modemIDs []uint) (map[[2]uint]*models.SimGrant, error) {
	q := s.db.Where("user_id IN ?", userIDs)
	if len(modemIDs) > 0 {
		q = q.Where("modem_id IN ?", modemIDs)
	}
	var grants []models.SimGrant
	if err := q.Find(&grants).Error; err != nil {
		return nil, err
	}
	gm := make(map[[2]uint]*models.SimGrant, len(grants))
	for i := range grants {
		gm[[2]uint{grants[i].UserID, grants[i].ModemID}] = &grants[i]
	}
	return gm, nil
}

// validLevel 检查权限级别是否合法（view 或 use）。
func validLevel(level string) bool {
	return level == models.LevelView || level == models.LevelUse
}

// setNoteField 将审批备注写入申请记录，空字符串时置 nil。
func setNoteField(r *models.SimAccessRequest, note string) {
	if note == "" {
		r.AdminNote = nil
	} else {
		r.AdminNote = &note
	}
}

// mapKeys 将 map[uint]bool 的所有键提取为 slice。
func mapKeys(m map[uint]bool) []uint {
	out := make([]uint, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
