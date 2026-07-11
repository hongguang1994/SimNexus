package services

// TelegramHub 是 Telegram 消息的实时广播中枢：收/发一条 Telegram 消息入库后，把该消息 JSON
// 广播给所有已连接的 WebSocket 客户端，实现 Telegram 页面的实时推送（替代 5 秒轮询延迟）。
// 与 MessageHub 同构，但负载是 models.TelegramMessage（字段带 json tag，直接对齐前端类型）。

import (
	"encoding/json"
	"sync"

	"simnexus-go/models"
)

type TelegramHub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

var telegramHub = &TelegramHub{subs: map[chan []byte]struct{}{}}

// GetTelegramHub 返回全局 Telegram 广播中枢。
func GetTelegramHub() *TelegramHub { return telegramHub }

// Subscribe 注册一个订阅者，返回其接收通道。
func (h *TelegramHub) Subscribe() chan []byte {
	ch := make(chan []byte, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe 注销并关闭订阅通道。
func (h *TelegramHub) Unsubscribe(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// Broadcast 把一条 Telegram 消息广播给所有订阅者（通道满则丢弃，避免阻塞；前端仍有轮询兜底）。
func (h *TelegramHub) Broadcast(m *models.TelegramMessage) {
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

// BroadcastTelegram 供 service/handler 层入库后调用。
func BroadcastTelegram(m *models.TelegramMessage) { telegramHub.Broadcast(m) }
