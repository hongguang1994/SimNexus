package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"simnexus-go/database"
	"simnexus-go/models"
	"simnexus-go/security"
)

// telegramAPIBase Telegram Bot API 基础 URL。
const telegramAPIBase = "https://api.telegram.org"

var (
	tgLastUpdateID int64                           // 长轮询 offset，避免重复处理同一消息
	tgReModemArg   = regexp.MustCompile(`^#(\d+)`) // 匹配 /send #<id> 中的设备 ID
)

// tgChatAllowed 判断某 chat_id 是否在命令白名单内（fail-closed：白名单为空则拒绝所有）。
// 白名单来自有效配置（DB 优先、env 兜底，见 telegram_settings.go）。
func tgChatAllowed(chatID string) bool {
	for _, id := range tgSet().allowed {
		if id == chatID {
			return true
		}
	}
	return false
}

func tgToken() string   { return tgSet().token }
func tgChatID() string  { return tgSet().pushID }
func tgBaseURL() string { return fmt.Sprintf("%s/bot%s", telegramAPIBase, tgToken()) }

// ── Telegram 账号绑定：网页生成一次性绑定码，Telegram 里 /bind <码> 绑定 ──

type tgBindCode struct {
	userID uint
	exp    time.Time
}

var (
	tgBindCodes  = map[string]tgBindCode{}
	tgBindCodeMu sync.Mutex
)

// TelegramGenBindCode 为某用户生成一次性绑定码（10 分钟有效）。
func TelegramGenBindCode(userID uint) string {
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // 去掉易混字符
	b := make([]byte, 6)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[n.Int64()]
	}
	code := string(b)
	tgBindCodeMu.Lock()
	// 清理过期码
	now := time.Now()
	for k, v := range tgBindCodes {
		if now.After(v.exp) {
			delete(tgBindCodes, k)
		}
	}
	tgBindCodes[code] = tgBindCode{userID: userID, exp: now.Add(10 * time.Minute)}
	tgBindCodeMu.Unlock()
	return code
}

// tgConsumeBindCode 校验并消费绑定码，返回用户 ID。
func tgConsumeBindCode(code string) (uint, bool) {
	tgBindCodeMu.Lock()
	defer tgBindCodeMu.Unlock()
	c, ok := tgBindCodes[code]
	if !ok || time.Now().After(c.exp) {
		return 0, false
	}
	delete(tgBindCodes, code)
	return c.userID, true
}

// tgBoundUser 返回某 chat 绑定的用户 ID（0=未绑定）。
func tgBoundUser(chatID string) uint {
	var b models.TelegramBind
	if database.DB.Where("chat_id = ?", chatID).First(&b).Error == nil {
		return b.UserID
	}
	return 0
}

const tgNeedBind = "请先绑定账号：/bind &lt;绑定码&gt;\n（在网页右上角「绑定 Telegram」生成绑定码）"

// tgLoadUser 载入某 chat 绑定的用户（带 RBAC 角色与设备范围）。未绑定或用户不存在返回 nil。
func tgLoadUser(chatID string) *models.User {
	uid := tgBoundUser(chatID)
	if uid == 0 {
		return nil
	}
	var u models.User
	if database.DB.Preload("RbacRoles").Preload("RbacRoles.ModemScope").
		Where("is_active = ?", true).First(&u, uid).Error != nil {
		return nil
	}
	return &u
}

// tgVisibleModems 返回该用户有权查看的设备（管理员=全部；无查看权限=空）。复用网页同一套权限逻辑。
func tgVisibleModems(u *models.User) []models.Modem {
	svc := NewModemService(database.DB)
	ms, _ := svc.ListUserModems(u)
	return ms
}

// tgSendableSet 返回该用户可发送短信（use 级）的设备 ID 集合。管理员=全部。
func tgSendableSet(u *models.User) map[uint]bool {
	set := map[uint]bool{}
	if u.IsAdmin() {
		var all []models.Modem
		database.DB.Select("id").Find(&all)
		for _, m := range all {
			set[m.ID] = true
		}
		return set
	}
	for _, id := range security.GetUserModemGrants(database.DB, u.ID, models.LevelUse, u) {
		set[id] = true
	}
	return set
}

// TelegramSendMessage sends a text message to a chat. Returns success.
func TelegramSendMessage(text, chatID string, logIt bool) bool {
	if tgToken() == "" || (chatID == "" && tgChatID() == "") {
		return false
	}
	target := chatID
	if target == "" {
		target = tgChatID()
	}
	payload, _ := json.Marshal(map[string]string{
		"chat_id": target, "text": text, "parse_mode": "HTML",
	})
	resp, err := http.Post(tgBaseURL()+"/sendMessage", "application/json", bytes.NewReader(payload))
	if err != nil {
		slog.Error("telegram sendMessage failed", "err", err)
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r struct {
		Ok bool `json:"ok"`
	}
	json.Unmarshal(body, &r)
	if r.Ok && logIt {
		tgLog(target, "SimNexus", "out", text, false, "", "")
	}
	return r.Ok
}

// TelegramPushInboundSMS notifies the Telegram chat of a received SMS.
func TelegramPushInboundSMS(modemLabel, sender, content string) bool {
	text := fmt.Sprintf("📨 <b>收到短信</b>\n设备：%s\n发件人：%s\n内容：%s", modemLabel, sender, content)
	return TelegramSendMessage(text, "", true)
}

// tgLog 将 Telegram 消息持久化到数据库，供前端展示会话记录。
func tgLog(chatID, username, direction, text string, isCmd bool, fileID, fileType string) {
	var up *string
	if username != "" {
		up = &username
	}
	var fid, ft *string
	if fileID != "" {
		fid = &fileID
	}
	if fileType != "" {
		ft = &fileType
	}
	rec := &models.TelegramMessage{
		ChatID: chatID, Username: up, Direction: direction, Text: text,
		IsCommand: isCmd, FileID: fid, FileType: ft, CreatedAt: time.Now(),
	}
	database.DB.Create(rec)
	BroadcastTelegram(rec) // WebSocket 实时推送给 Telegram 页面
}

// modemLabel 返回设备展示名称，用于 Bot 消息中的设备标识。
func modemLabel(m *models.Modem) string {
	if m.Alias != "" {
		return m.Alias
	}
	if m.Model != "" {
		return m.Model
	}
	return fmt.Sprintf("设备#%d", m.ID)
}

// tgDoSend 通过指定设备发送短信，并将结果回复到 Telegram。
// ZTE 设备走 HTTP 驱动，其余走 mmcli。
func tgDoSend(m *models.Modem, number, content, chatID string) {
	obj := m.MmObjectPath
	var success bool
	var errMsg string
	switch {
	case m.VowifiMode:
		// VoWiFi 模式的卡已被 mmcli --inhibit 独占，必须走常驻 VoWiFi 会话发送，
		// 否则打到被独占的 mmcli 上必然失败（与网页/定时任务发送路径保持一致）。
		success, errMsg = GetVowifiManager().SendSMS(m, number, content)
	case strings.HasPrefix(obj, "zte:"):
		success = ZteSendSMS(number, content)
		if !success {
			errMsg = "ZTE send failed"
		}
	default:
		mm := reModemIdx.FindStringSubmatch(obj)
		if mm == nil {
			TelegramSendMessage("❌ 无法获取设备索引", chatID, true)
			return
		}
		success, errMsg = SendSMS(mm[1], number, content)
	}
	label := modemLabel(m)
	if success {
		TelegramSendMessage(fmt.Sprintf("✅ [%s] 短信已发送至 %s", label, number), chatID, true)
	} else {
		TelegramSendMessage(fmt.Sprintf("❌ [%s] 发送失败：%s", label, errMsg), chatID, true)
	}
}

type tgUpdate struct {
	UpdateID      int64      `json:"update_id"`
	Message       *tgMessage `json:"message"`
	EditedMessage *tgMessage `json:"edited_message"`
}

type tgMessage struct {
	Text    string                 `json:"text"`
	Caption string                 `json:"caption"`
	Chat    struct{ ID int64 }     `json:"chat"`
	From    map[string]interface{} `json:"from"`
	Photo   []struct {
		FileID string `json:"file_id"`
	} `json:"photo"`
	Document map[string]interface{} `json:"document"`
	Video    map[string]interface{} `json:"video"`
	Sticker  map[string]interface{} `json:"sticker"`
	Voice    map[string]interface{} `json:"voice"`
}

// tgUsername 从 Telegram 消息的 from 字段提取用户名或姓名。
func tgUsername(from map[string]interface{}) string {
	if from == nil {
		return ""
	}
	if u, ok := from["username"].(string); ok && u != "" {
		return u
	}
	if u, ok := from["first_name"].(string); ok {
		return u
	}
	return ""
}

// tgHandle 处理收到的 Telegram 消息：分发媒体类消息入库，解析文字命令执行操作。
// 支持命令：/modems /send /list /help。
func tgHandle(msg *tgMessage) {
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	text := strings.TrimSpace(firstNonEmpty(msg.Text, msg.Caption))
	username := tgUsername(msg.From)

	// 授权校验：Bot 的用户名是公开可搜索的，任何人都能私聊它。若不校验发件人，陌生人就能
	// 用 /list 读你的短信、用 /send 拿你的卡发短信。只处理白名单内 chat 的消息，其余忽略。
	// /bind /start /help 对任何人开放（绑定是获取授权的入口）；其余命令要求已在白名单或已绑定账号。
	openCmd := strings.HasPrefix(text, "/bind") || strings.HasPrefix(text, "/start") || strings.HasPrefix(text, "/help")
	if !openCmd && !tgChatAllowed(chatID) && tgBoundUser(chatID) == 0 {
		slog.Warn("telegram: 忽略未授权 chat 的消息", "chat_id", chatID, "user", username, "text", text)
		return
	}

	// media handling
	if len(msg.Photo) > 0 {
		tgLog(chatID, username, "in", orDefault(text, "[图片]"), false, msg.Photo[len(msg.Photo)-1].FileID, "photo")
		return
	}
	if fid := mapFileID(msg.Document); fid != "" {
		fname, _ := msg.Document["file_name"].(string)
		tgLog(chatID, username, "in", orDefault(text, "[文件: "+fname+"]"), false, fid, "document")
		return
	}
	if fid := mapFileID(msg.Video); fid != "" {
		tgLog(chatID, username, "in", orDefault(text, "[视频]"), false, fid, "video")
		return
	}
	if fid := mapFileID(msg.Sticker); fid != "" {
		tgLog(chatID, username, "in", "[贴纸]", false, fid, "sticker")
		return
	}
	if fid := mapFileID(msg.Voice); fid != "" {
		tgLog(chatID, username, "in", "[语音]", false, fid, "voice")
		return
	}

	if text == "" {
		return
	}
	tgLog(chatID, username, "in", text, strings.HasPrefix(text, "/"), "", "")

	switch {
	case strings.HasPrefix(text, "/modems"):
		u := tgLoadUser(chatID)
		if u == nil {
			TelegramSendMessage(tgNeedBind, chatID, true)
			return
		}
		modems := tgVisibleModems(u)
		if len(modems) == 0 {
			TelegramSendMessage("你没有可访问的设备", chatID, true)
			return
		}
		lines := []string{"📱 <b>当前设备列表</b>"}
		for _, m := range modems {
			st := "🔴"
			if m.Status == models.ModemConnected {
				st = "🟢"
			}
			phone := ""
			if m.PhoneNumber != "" {
				phone = " " + m.PhoneNumber
			}
			op := ""
			if m.Operator != "" {
				op = " [" + m.Operator + "]"
			}
			lines = append(lines, fmt.Sprintf("%s <b>#%d</b> %s%s%s", st, m.ID, modemLabel(&m), phone, op))
		}
		lines = append(lines, "\n发送时用: /send #&lt;设备ID&gt; &lt;号码&gt; &lt;内容&gt;")
		TelegramSendMessage(strings.Join(lines, "\n"), chatID, true)

	case strings.HasPrefix(text, "/send"):
		u := tgLoadUser(chatID)
		if u == nil {
			TelegramSendMessage(tgNeedBind, chatID, true)
			return
		}
		if !u.IsAdmin() {
			if p := security.Perm(u); p == nil || p.ReadOnly {
				TelegramSendMessage("你没有发送短信的权限", chatID, true)
				return
			}
		}
		sendable := tgSendableSet(u) // 该用户可发送(use 级)的设备
		args := strings.TrimSpace(text[5:])
		var modem *models.Modem
		if m := tgReModemArg.FindStringSubmatch(args); m != nil {
			mid, _ := strconv.Atoi(m[1])
			if !sendable[uint(mid)] {
				TelegramSendMessage(fmt.Sprintf("❌ 你无权用设备 #%d 发送", mid), chatID, true)
				return
			}
			var mm models.Modem
			if err := database.DB.First(&mm, mid).Error; err != nil {
				TelegramSendMessage(fmt.Sprintf("❌ 未找到设备 #%d", mid), chatID, true)
				return
			}
			modem = &mm
			args = strings.TrimSpace(args[len(m[0]):])
		}
		parts := strings.SplitN(args, " ", 2)
		if len(parts) < 2 {
			TelegramSendMessage("用法:\n/send &lt;号码&gt; &lt;内容&gt;\n/send #&lt;设备ID&gt; &lt;号码&gt; &lt;内容&gt;", chatID, true)
			return
		}
		number, content := parts[0], parts[1]
		if modem == nil {
			// 从「你可发送 + 在线」的设备里挑
			var connected []models.Modem
			database.DB.Where("status = ?", models.ModemConnected).Find(&connected)
			var avail []models.Modem
			for _, m := range connected {
				if sendable[m.ID] {
					avail = append(avail, m)
				}
			}
			if len(avail) == 0 {
				TelegramSendMessage("❌ 无可用设备（你有权发送的卡都不在线）", chatID, true)
				return
			}
			if len(avail) > 1 {
				lines := []string{"⚠️ 有多个可用设备，请指定设备ID："}
				for _, m := range avail {
					lines = append(lines, fmt.Sprintf("  #%d %s", m.ID, modemLabel(&m)))
				}
				lines = append(lines, fmt.Sprintf("\n例: /send #%d %s %s", avail[0].ID, number, content))
				TelegramSendMessage(strings.Join(lines, "\n"), chatID, true)
				return
			}
			modem = &avail[0]
		}
		tgDoSend(modem, number, content, chatID)

	case strings.HasPrefix(text, "/list"):
		u := tgLoadUser(chatID)
		if u == nil {
			TelegramSendMessage(tgNeedBind, chatID, true)
			return
		}
		if !u.IsAdmin() {
			if p := security.Perm(u); p == nil || !p.CanViewHistory {
				TelegramSendMessage("你没有查看短信记录的权限", chatID, true)
				return
			}
		}
		vis := map[uint]bool{}
		for _, m := range tgVisibleModems(u) {
			vis[m.ID] = true
		}
		if len(vis) == 0 {
			TelegramSendMessage("你没有可访问的设备", chatID, true)
			return
		}
		args := strings.TrimSpace(text[5:])
		q := database.DB.Where("direction = ?", models.SmsInbound)
		if strings.HasPrefix(args, "#") {
			if mid, err := strconv.Atoi(strings.Fields(args[1:])[0]); err == nil {
				if !vis[uint(mid)] {
					TelegramSendMessage("你无权查看该设备的短信", chatID, true)
					return
				}
				q = q.Where("modem_id = ?", mid)
			}
		} else {
			ids := make([]uint, 0, len(vis))
			for id := range vis {
				ids = append(ids, id)
			}
			q = q.Where("modem_id IN ?", ids)
		}
		var msgs []models.SmsMessage
		q.Order("created_at desc").Limit(10).Find(&msgs)
		if len(msgs) == 0 {
			TelegramSendMessage("暂无收到的短信", chatID, true)
			return
		}
		var modems []models.Modem
		database.DB.Find(&modems)
		labelMap := map[uint]string{}
		for i := range modems {
			labelMap[modems[i].ID] = modemLabel(&modems[i])
		}
		lines := []string{"📋 <b>最近收到的短信</b>"}
		for _, m := range msgs {
			ts := m.CreatedAt.Format("01-02 15:04")
			dev := labelMap[m.ModemID]
			if dev == "" {
				dev = fmt.Sprintf("#%d", m.ModemID)
			}
			lines = append(lines, fmt.Sprintf("\n[%s] <b>%s</b> via %s\n%s", ts, m.PhoneNumber, dev, m.Content))
		}
		TelegramSendMessage(strings.Join(lines, "\n"), chatID, true)

	case strings.HasPrefix(text, "/bind"):
		arg := strings.ToUpper(strings.TrimSpace(text[len("/bind"):]))
		if arg == "" {
			TelegramSendMessage("用法：/bind &lt;绑定码&gt;\n绑定码在网页「Telegram 绑定」处生成（10 分钟有效）。", chatID, true)
			return
		}
		uid, ok := tgConsumeBindCode(arg)
		if !ok {
			TelegramSendMessage("❌ 绑定码无效或已过期", chatID, true)
			return
		}
		var u models.User
		if database.DB.First(&u, uid).Error != nil {
			TelegramSendMessage("❌ 账号不存在", chatID, true)
			return
		}
		database.DB.Where("chat_id = ?", chatID).Delete(&models.TelegramBind{})
		database.DB.Create(&models.TelegramBind{ChatID: chatID, UserID: uid})
		TelegramSendMessage("✅ 已绑定账号 <b>"+htmlEscape(u.Username)+"</b>\n现在 /contacts 会返回你的通讯录。", chatID, true)

	case strings.HasPrefix(text, "/unbind"):
		database.DB.Where("chat_id = ?", chatID).Delete(&models.TelegramBind{})
		TelegramSendMessage("已解绑该 chat。", chatID, true)

	case strings.HasPrefix(text, "/whoami"):
		uid := tgBoundUser(chatID)
		if uid == 0 {
			TelegramSendMessage("未绑定账号。发送 /bind &lt;绑定码&gt; 绑定。", chatID, true)
			return
		}
		var u models.User
		database.DB.First(&u, uid)
		TelegramSendMessage("已绑定账号：<b>"+htmlEscape(u.Username)+"</b>", chatID, true)

	case strings.HasPrefix(text, "/contacts"):
		uid := tgBoundUser(chatID)
		if uid == 0 {
			TelegramSendMessage("请先绑定账号：/bind &lt;绑定码&gt;（网页生成）", chatID, true)
			return
		}
		kw := strings.TrimSpace(text[len("/contacts"):])
		q := database.DB.Where("owner_id = ?", uid)
		if kw != "" {
			like := "%" + kw + "%"
			q = q.Where("name LIKE ? OR phone LIKE ? OR company LIKE ?", like, like, like)
		}
		var cs []models.Contact
		q.Order("name asc").Limit(50).Find(&cs)
		if len(cs) == 0 {
			TelegramSendMessage("通讯录为空"+ternary(kw != "", "（无匹配「"+htmlEscape(kw)+"」）", ""), chatID, true)
			return
		}
		lines := []string{fmt.Sprintf("📇 <b>通讯录</b>（%d）", len(cs))}
		for _, c := range cs {
			name := c.Name
			if name == "" {
				name = c.Phone
			}
			line := "• <b>" + htmlEscape(name) + "</b>"
			if c.Phone != "" {
				line += "  " + htmlEscape(c.Phone)
			}
			if c.Company != "" {
				line += " · " + htmlEscape(c.Company)
			}
			lines = append(lines, line)
		}
		TelegramSendMessage(strings.Join(lines, "\n"), chatID, true)

	case strings.HasPrefix(text, "/start"), strings.HasPrefix(text, "/help"):
		TelegramSendMessage(tgHelpText(), chatID, true)
	default:
		tgTryCustom(chatID, text)
	}
}

// htmlEscape 转义 Telegram HTML 解析模式下的特殊字符。
func htmlEscape(s string) string { return html.EscapeString(s) }

// ternary 是简单的三元表达式辅助。
func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// tgHelpText 拼装帮助文本：内置命令 + 已启用的自定义命令。
func tgHelpText() string {
	help := "🤖 <b>SimNexus Bot</b>\n\n" +
		"/modems - 查看所有设备\n" +
		"/list - 查看最近10条收到的短信\n" +
		"/list #&lt;设备ID&gt; - 查看指定设备的短信\n" +
		"/send &lt;号码&gt; &lt;内容&gt; - 发送短信（单卡时自动选择）\n" +
		"/send #&lt;设备ID&gt; &lt;号码&gt; &lt;内容&gt; - 通过指定设备发送\n" +
		"\n<b>账号</b>\n" +
		"/bind &lt;绑定码&gt; - 绑定网页账号（绑定码在网页生成）\n" +
		"/whoami - 查看当前绑定的账号\n" +
		"/unbind - 解除绑定\n" +
		"/contacts [关键词] - 查看你的通讯录\n"
	var cmds []models.TelegramCommand
	database.DB.Where("enabled = ?", true).Order("command asc").Find(&cmds)
	if len(cmds) > 0 {
		help += "\n<b>自定义命令</b>\n"
		for _, c := range cmds {
			desc := c.Description
			if desc == "" {
				switch c.Type {
				case "send":
					desc = "发送预设短信"
				case "webhook":
					desc = "外部命令"
				default:
					desc = "自动回复"
				}
			}
			help += fmt.Sprintf("/%s - %s\n", c.Command, desc)
		}
	}
	return help
}

// tgTryCustom 匹配并执行自定义命令（内置命令未命中时兜底）。
func tgTryCustom(chatID, text string) {
	if !strings.HasPrefix(text, "/") {
		return
	}
	fields := strings.Fields(text)
	name := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
	if i := strings.Index(name, "@"); i >= 0 { // 群里可能是 /cmd@BotName
		name = name[:i]
	}
	var cmd models.TelegramCommand
	if database.DB.Where("lower(command) = ? AND enabled = ?", name, true).First(&cmd).Error != nil {
		TelegramSendMessage("未知命令，发送 /help 查看可用命令", chatID, true)
		return
	}
	switch cmd.Type {
	case "reply":
		TelegramSendMessage(cmd.ReplyText, chatID, true)
	case "send":
		number := strings.TrimSpace(cmd.ToNumber)
		if len(fields) >= 2 { // 命令参数优先于预设号码
			number = fields[1]
		}
		if number == "" {
			TelegramSendMessage("该命令需要号码参数：/"+cmd.Command+" &lt;号码&gt;", chatID, true)
			return
		}
		var modem *models.Modem
		if cmd.ModemID > 0 {
			var mm models.Modem
			if database.DB.First(&mm, cmd.ModemID).Error != nil {
				TelegramSendMessage(fmt.Sprintf("❌ 预设设备 #%d 不存在", cmd.ModemID), chatID, true)
				return
			}
			modem = &mm
		} else {
			var connected []models.Modem
			database.DB.Where("status = ?", models.ModemConnected).Find(&connected)
			if len(connected) == 0 {
				TelegramSendMessage("❌ 无可用设备", chatID, true)
				return
			}
			modem = &connected[0]
		}
		tgDoSend(modem, number, cmd.Content, chatID)
	case "webhook":
		tgRunWebhook(&cmd, fields, text, chatID)
	default:
		TelegramSendMessage("命令类型无效", chatID, true)
	}
}

// tgRunWebhook 把命令转发到外部 HTTP 服务，用返回体（或 JSON 的 text 字段）作为回复。
// 让「页面配置即可实现任意命令」——逻辑写在外部服务，无需改后台。URL 由管理员配置。
func tgRunWebhook(cmd *models.TelegramCommand, fields []string, text, chatID string) {
	if strings.TrimSpace(cmd.WebhookURL) == "" {
		TelegramSendMessage("该命令未配置 Webhook URL", chatID, true)
		return
	}
	args := []string{}
	if len(fields) > 1 {
		args = fields[1:]
	}
	payload, _ := json.Marshal(map[string]any{
		"command": cmd.Command,
		"args":    args,
		"text":    text,
		"chat_id": chatID,
	})
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(cmd.WebhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		TelegramSendMessage("❌ Webhook 调用失败："+err.Error(), chatID, true)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	reply := strings.TrimSpace(string(body))
	// 优先解析 {"text": "..."}
	var j struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(body, &j) == nil && strings.TrimSpace(j.Text) != "" {
		reply = j.Text
	}
	if reply == "" {
		reply = "（Webhook 无返回内容）"
	}
	TelegramSendMessage(reply, chatID, true)
}

// mapFileID 从媒体对象（document/video/sticker/voice）中提取 file_id。
func mapFileID(m map[string]interface{}) string {
	if m == nil {
		return ""
	}
	if v, ok := m["file_id"].(string); ok {
		return v
	}
	return ""
}

// orDefault 若 s 为空则返回 def，用于媒体消息的展示文本兜底。
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// StartTelegramPolling 载入有效配置并按当前 token 启动长轮询；token 变更后可经
// RestartTelegramPolling 热重启（见 telegram_settings.go）。parent ctx 取消则整体停止。
func StartTelegramPolling(parent context.Context) {
	LoadTelegramSettings()
	tgPollMu.Lock()
	tgPollParent = parent
	tgPollMu.Unlock()
	if tgToken() == "" {
		slog.Warn("Telegram bot token not configured; skipping polling")
		return
	}
	RestartTelegramPolling()
}

// tgPollLoop 长轮询 getUpdates 直到 ctx 取消。
func tgPollLoop(ctx context.Context) {
	slog.Info("Telegram bot polling started")
	client := &http.Client{Timeout: 35 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		u := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=30", tgBaseURL(), tgLastUpdateID+1)
		resp, err := client.Get(u)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var data struct {
			Ok     bool       `json:"ok"`
			Result []tgUpdate `json:"result"`
		}
		if json.Unmarshal(body, &data) != nil || !data.Ok {
			continue
		}
		for _, up := range data.Result {
			tgLastUpdateID = up.UpdateID
			msg := up.Message
			if msg == nil {
				msg = up.EditedMessage
			}
			if msg != nil {
				go tgHandle(msg)
			}
		}
	}
}

// TelegramProxyFile downloads a Telegram file, returning content, content-type, filename.
func TelegramProxyFile(fileID string) ([]byte, string, string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	r, err := client.Get(fmt.Sprintf("%s/getFile?file_id=%s", tgBaseURL(), url.QueryEscape(fileID)))
	if err != nil {
		return nil, "", "", err
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	var data struct {
		Ok     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if json.Unmarshal(body, &data) != nil || !data.Ok {
		return nil, "", "", fmt.Errorf("file not found")
	}
	fileURL := fmt.Sprintf("%s/file/bot%s/%s", telegramAPIBase, tgToken(), data.Result.FilePath)
	resp, err := client.Get(fileURL)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	content, _ := io.ReadAll(resp.Body)
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	parts := strings.Split(data.Result.FilePath, "/")
	return content, ct, parts[len(parts)-1], nil
}

// TelegramSendFile uploads a photo/document to the configured chat.
func TelegramSendFile(filename string, content []byte, contentType, caption string) (bool, string, string, string) {
	if tgToken() == "" || tgChatID() == "" {
		return false, "", "", "Bot not configured"
	}
	isImage := strings.HasPrefix(contentType, "image/")
	method := "sendDocument"
	field := "document"
	fileType := "document"
	if isImage {
		method, field, fileType = "sendPhoto", "photo", "photo"
	}

	var buf bytes.Buffer
	boundary := "----simnexusboundary"
	writePart := func(name, value string) {
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString(fmt.Sprintf("Content-Disposition: form-data; name=%q\r\n\r\n", name))
		buf.WriteString(value + "\r\n")
	}
	writePart("chat_id", tgChatID())
	writePart("caption", caption)
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString(fmt.Sprintf("Content-Disposition: form-data; name=%q; filename=%q\r\n", field, filename))
	buf.WriteString("Content-Type: " + contentType + "\r\n\r\n")
	buf.Write(content)
	buf.WriteString("\r\n--" + boundary + "--\r\n")

	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/%s", tgBaseURL(), method), &buf)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return false, "", "", err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var data struct {
		Ok          bool                   `json:"ok"`
		Description string                 `json:"description"`
		Result      map[string]interface{} `json:"result"`
	}
	json.Unmarshal(body, &data)
	if !data.Ok {
		return false, "", "", data.Description
	}
	sentFileID := ""
	if isImage {
		if arr, ok := data.Result["photo"].([]interface{}); ok && len(arr) > 0 {
			if last, ok := arr[len(arr)-1].(map[string]interface{}); ok {
				sentFileID, _ = last["file_id"].(string)
			}
		}
	} else if doc, ok := data.Result["document"].(map[string]interface{}); ok {
		sentFileID, _ = doc["file_id"].(string)
	}
	return true, fileType, sentFileID, ""
}
