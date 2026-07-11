package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"simnexus-go/config"
	"simnexus-go/database"
	"simnexus-go/models"
)

// telegramAPIBase Telegram Bot API 基础 URL。
const telegramAPIBase = "https://api.telegram.org"

var (
	tgLastUpdateID int64                           // 长轮询 offset，避免重复处理同一消息
	tgReModemArg   = regexp.MustCompile(`^#(\d+)`) // 匹配 /send #<id> 中的设备 ID
)

// tgChatAllowed 判断某 chat_id 是否在命令白名单内（fail-closed：白名单为空则拒绝所有）。
func tgChatAllowed(chatID string) bool {
	for _, id := range config.C.TelegramAllowedChats {
		if id == chatID {
			return true
		}
	}
	return false
}

func tgToken() string   { return config.C.TelegramBotToken }
func tgChatID() string  { return config.C.TelegramChatID }
func tgBaseURL() string { return fmt.Sprintf("%s/bot%s", telegramAPIBase, tgToken()) }

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
	if !tgChatAllowed(chatID) {
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
		var modems []models.Modem
		database.DB.Where("is_active = ?", true).Find(&modems)
		if len(modems) == 0 {
			TelegramSendMessage("暂无设备", chatID, true)
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
		args := strings.TrimSpace(text[5:])
		var modem *models.Modem
		if m := tgReModemArg.FindStringSubmatch(args); m != nil {
			mid, _ := strconv.Atoi(m[1])
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
			var connected []models.Modem
			database.DB.Where("status = ?", models.ModemConnected).Find(&connected)
			if len(connected) == 0 {
				TelegramSendMessage("❌ 无可用设备", chatID, true)
				return
			}
			if len(connected) > 1 {
				lines := []string{"⚠️ 有多个在线设备，请指定设备ID："}
				for _, m := range connected {
					lines = append(lines, fmt.Sprintf("  #%d %s", m.ID, modemLabel(&m)))
				}
				lines = append(lines, fmt.Sprintf("\n例: /send #%d %s %s", connected[0].ID, number, content))
				TelegramSendMessage(strings.Join(lines, "\n"), chatID, true)
				return
			}
			modem = &connected[0]
		}
		tgDoSend(modem, number, content, chatID)

	case strings.HasPrefix(text, "/list"):
		args := strings.TrimSpace(text[5:])
		q := database.DB.Where("direction = ?", models.SmsInbound)
		if strings.HasPrefix(args, "#") {
			if mid, err := strconv.Atoi(strings.Fields(args[1:])[0]); err == nil {
				q = q.Where("modem_id = ?", mid)
			}
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

	case strings.HasPrefix(text, "/start"), strings.HasPrefix(text, "/help"):
		help := "🤖 <b>SimNexus Bot</b>\n\n" +
			"/modems - 查看所有设备\n" +
			"/list - 查看最近10条收到的短信\n" +
			"/list #&lt;设备ID&gt; - 查看指定设备的短信\n" +
			"/send &lt;号码&gt; &lt;内容&gt; - 发送短信（单卡时自动选择）\n" +
			"/send #&lt;设备ID&gt; &lt;号码&gt; &lt;内容&gt; - 通过指定设备发送\n"
		TelegramSendMessage(help, chatID, true)
	}
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

// StartTelegramPolling begins long-polling getUpdates until ctx is cancelled.
func StartTelegramPolling(ctx context.Context) {
	if tgToken() == "" {
		slog.Warn("Telegram bot token not configured; skipping polling")
		return
	}
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
