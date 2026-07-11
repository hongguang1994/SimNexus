package services

// MessageHub 是一个轻量的短信实时广播中枢：MT 收到 / MO 发出时把消息 JSON 广播给所有
// 已连接的 WebSocket 客户端，实现消息中心的实时推送（替代前端轮询的延迟）。

import (
	"encoding/json"
	"sync"

	"simnexus-go/models"
)

// MessageEvent 是推送给前端的一条消息事件（字段与前端 SmsMessage 对齐）。
type MessageEvent struct {
	ID          uint   `json:"id"`
	ModemID     uint   `json:"modem_id"`
	Direction   string `json:"direction"`
	Channel     string `json:"channel"`
	PhoneNumber string `json:"phone_number"`
	Content     string `json:"content"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

type MessageHub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

var messageHub = &MessageHub{subs: map[chan []byte]struct{}{}}

// GetMessageHub 返回全局消息广播中枢。
func GetMessageHub() *MessageHub { return messageHub }

// Subscribe 注册一个订阅者，返回其接收通道。
func (h *MessageHub) Subscribe() chan []byte {
	ch := make(chan []byte, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe 注销并关闭订阅通道。
func (h *MessageHub) Unsubscribe(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// Broadcast 把一条消息广播给所有订阅者（通道满则丢弃，避免阻塞）。
func (h *MessageHub) Broadcast(m *models.SmsMessage) {
	ev := MessageEvent{
		ID: m.ID, ModemID: m.ModemID, Direction: m.Direction, Channel: m.Channel,
		PhoneNumber: m.PhoneNumber, Content: m.Content, Status: m.Status,
		CreatedAt: m.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.mu.Lock()
	for ch := range h.subs {
		select {
		case ch <- data:
		default: // 订阅者处理慢，丢弃这条（前端仍有 3s 轮询兜底）
		}
	}
	h.mu.Unlock()
}

// broadcastMessage 是内部便捷调用。
func broadcastMessage(m *models.SmsMessage) { messageHub.Broadcast(m) }
