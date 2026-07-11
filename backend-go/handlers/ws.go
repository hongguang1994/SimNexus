package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"simnexus-go/database"
	"simnexus-go/models"
	"simnexus-go/security"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// MessageWS 通过 WebSocket 把新短信（MT 收 / MO 发）实时推给消息中心，替代前端轮询的延迟。
// 订阅广播中枢，按用户可见设备过滤后转发。
func MessageWS(c *gin.Context) {
	token := c.Query("token")
	username, err := security.ParseToken(token)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	user, err := security.LoadUserByUsername(database.DB, username)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	unrestricted := user.IsAdmin()
	visible := map[uint]bool{}
	if !unrestricted {
		for _, id := range security.GetUserModemGrants(database.DB, user.ID, "", user) {
			visible[id] = true
		}
	}

	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := services.GetMessageHub().Subscribe()
	defer services.GetMessageHub().Unsubscribe(ch)

	// 读泵：丢弃客户端消息，检测断开后关闭连接以唤醒写循环
	go func() {
		for {
			if _, _, e := conn.ReadMessage(); e != nil {
				conn.Close()
				return
			}
		}
	}()

	for data := range ch {
		if !unrestricted {
			var meta struct {
				ModemID uint `json:"modem_id"`
			}
			if json.Unmarshal(data, &meta) == nil && !visible[meta.ModemID] {
				continue // 无权查看该设备的消息，跳过
			}
		}
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return
		}
	}
}

// TelegramWS 把新的 Telegram 消息（收/发）实时推给管理端 Telegram 页面，替代 5 秒轮询。
// 仅管理员可订阅（与 Telegram 相关接口一致）。
func TelegramWS(c *gin.Context) {
	token := c.Query("token")
	username, err := security.ParseToken(token)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	user, err := security.LoadUserByUsername(database.DB, username)
	if err != nil || !user.IsAdmin() {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := services.GetTelegramHub().Subscribe()
	defer services.GetTelegramHub().Unsubscribe(ch)

	go func() {
		for {
			if _, _, e := conn.ReadMessage(); e != nil {
				conn.Close()
				return
			}
		}
	}()

	for data := range ch {
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return
		}
	}
}

// SupportWS 把新的客服会话消息实时推给用户咨询页，替代 5 秒轮询。
// 客服/管理员收到全部会话消息；普通用户只收到属于自己会话(user_id==自己)的消息。
func SupportWS(c *gin.Context) {
	token := c.Query("token")
	username, err := security.ParseToken(token)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	user, err := security.LoadUserByUsername(database.DB, username)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	staff := security.IsSupportStaff(user)

	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := services.GetSupportHub().Subscribe()
	defer services.GetSupportHub().Unsubscribe(ch)

	go func() {
		for {
			if _, _, e := conn.ReadMessage(); e != nil {
				conn.Close()
				return
			}
		}
	}()

	for data := range ch {
		if !staff {
			var meta struct {
				UserID uint `json:"user_id"`
			}
			if json.Unmarshal(data, &meta) == nil && meta.UserID != user.ID {
				continue // 非本人会话，普通用户不可见
			}
		}
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return
		}
	}
}

// ModemStatusWS pushes modem state to the client every 5 seconds.
func ModemStatusWS(c *gin.Context) {
	token := c.Query("token")
	username, err := security.ParseToken(token)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	user, err := security.LoadUserByUsername(database.DB, username)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	var visibleIDs []uint
	unrestricted := user.IsAdmin()
	if !unrestricted {
		visibleIDs = security.GetUserModemGrants(database.DB, user.ID, "", user)
	}

	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	for {
		q := database.DB.Model(&models.Modem{}).Where("is_active = ?", true)
		if !unrestricted {
			if len(visibleIDs) == 0 {
				conn.WriteJSON([]interface{}{})
				time.Sleep(5 * time.Second)
				continue
			}
			q = q.Where("id IN ?", visibleIDs)
		}
		var modems []models.Modem
		q.Find(&modems)
		data := make([]gin.H, 0, len(modems))
		for _, m := range modems {
			status := m.Status
			if status == "" {
				status = models.ModemUnknown
			}
			data = append(data, gin.H{
				"id": m.ID, "alias": m.Alias, "device_path": m.DevicePath,
				"operator": m.Operator, "signal_quality": m.SignalQuality,
				"status": status, "phone_number": m.PhoneNumber,
			})
		}
		if err := conn.WriteJSON(data); err != nil {
			return
		}
		time.Sleep(5 * time.Second)
	}
}
