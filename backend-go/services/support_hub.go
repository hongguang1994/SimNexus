package services

// SupportHub 客服消息实时广播中枢：一条客服会话消息入库后广播给已连接的 WebSocket 客户端，
// 实现用户咨询页的实时推送（替代 5 秒轮询）。与 MessageHub/TelegramHub 同构。
// 负载是 models.SupportMessage（字段带 json tag，直接对齐前端类型）。

import (
	"encoding/json"
	"sync"

	"simnexus-go/models"
)

type SupportHub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

var supportHub = &SupportHub{subs: map[chan []byte]struct{}{}}

// GetSupportHub 返回全局客服广播中枢。
func GetSupportHub() *SupportHub { return supportHub }

// Subscribe 注册一个订阅者，返回其接收通道。
func (h *SupportHub) Subscribe() chan []byte {
	ch := make(chan []byte, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe 注销并关闭订阅通道。
func (h *SupportHub) Unsubscribe(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// Broadcast 把一条客服消息广播给所有订阅者（通道满则丢弃；前端仍有轮询兜底）。
func (h *SupportHub) Broadcast(m *models.SupportMessage) {
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	h.mu.Lock()
	for ch := range h.subs {
		select {
		case ch <- data:
		default:
		}
	}
	h.mu.Unlock()
}

// BroadcastSupport 供 service 层入库后调用。
func BroadcastSupport(m *models.SupportMessage) { supportHub.Broadcast(m) }
