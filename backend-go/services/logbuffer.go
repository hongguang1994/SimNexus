package services

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// LogEntry 是一条结构化日志记录，用于实时推送给前端（WebSocket / SSE）。
type LogEntry struct {
	Time     string `json:"time"`
	Level    string `json:"level"`
	Category string `json:"category"` // http/vowifi/poller/scheduler/telegram/system
	Msg      string `json:"msg"`
	Attrs    string `json:"attrs,omitempty"` // 附加属性 JSON，可能为空
}

// categorizeMsg 依据日志消息内容归类（用于没有显式 cat 属性的 slog 日志）。
func categorizeMsg(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "task") || strings.Contains(m, "cron") || strings.Contains(m, "scheduler"):
		return "scheduler"
	case strings.Contains(m, "telegram"):
		return "telegram"
	case strings.Contains(m, "modem") || strings.Contains(m, "dbus") || strings.Contains(m, "poller") ||
		strings.Contains(m, "airplane") || strings.Contains(m, "enable") || strings.Contains(m, "reset") ||
		strings.Contains(m, "sim-missing") || strings.Contains(m, "power"):
		return "poller"
	default:
		return "system"
	}
}

// AppendVowifi 供 VoWiFi 协议栈（非 slog 的 fmt 输出）把日志汇入缓冲区，归类为 vowifi。
func (b *LogBuffer) AppendVowifi(level, msg string) {
	if level == "" {
		level = "DEBUG"
	}
	b.Append(LogEntry{
		Time:     time.Now().UTC().Format(time.RFC3339),
		Level:    level,
		Category: "vowifi",
		Msg:      msg,
	})
}

// LogBuffer 环形日志缓冲区，保存最近 cap 条日志并向所有订阅者广播新日志。
type LogBuffer struct {
	mu    sync.Mutex
	lines []LogEntry      // 环形缓冲
	cap   int             // 最大保留条数
	subs  []chan LogEntry // 活跃订阅者 channel 列表
}

// GlobalLog 是全局日志缓冲单例，由 BufferedHandler 写入，由 LogsSSE 读取。
var GlobalLog = &LogBuffer{cap: 500}

// Append 向缓冲区追加一条日志，超出容量时丢弃最旧的一条，同时广播给所有订阅者。
func (b *LogBuffer) Append(e LogEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lines) >= b.cap {
		b.lines = b.lines[1:]
	}
	b.lines = append(b.lines, e)
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Snapshot returns all buffered lines.
func (b *LogBuffer) Snapshot() []LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]LogEntry, len(b.lines))
	copy(out, b.lines)
	return out
}

// Subscribe returns a channel that receives new entries until ctx is done.
func (b *LogBuffer) Subscribe(ctx context.Context) <-chan LogEntry {
	ch := make(chan LogEntry, 64)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		for i, s := range b.subs {
			if s == ch {
				b.subs = append(b.subs[:i], b.subs[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
		close(ch)
	}()
	return ch
}

// BufferedHandler 包装另一个 slog.Handler，将每条日志同时写入 GlobalLog 缓冲区，
// 实现结构化日志既输出到标准输出又可通过 SSE 实时推送给前端。
type BufferedHandler struct {
	inner slog.Handler
}

// NewBufferedHandler 创建 BufferedHandler，inner 为实际输出目标（如 slog.TextHandler）。
func NewBufferedHandler(inner slog.Handler) *BufferedHandler {
	return &BufferedHandler{inner: inner}
}

func (h *BufferedHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *BufferedHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := map[string]any{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	// 分类：优先取显式 cat 属性（如 HTTP 中间件打的 cat=http），否则按消息内容归类。
	cat := ""
	if c, ok := attrs["cat"].(string); ok && c != "" {
		cat = c
		delete(attrs, "cat") // 不重复展示在 attrs 里
	} else {
		cat = categorizeMsg(r.Message)
	}
	attrsStr := ""
	if len(attrs) > 0 {
		b, _ := json.Marshal(attrs)
		attrsStr = string(b)
	}
	GlobalLog.Append(LogEntry{
		Time:     r.Time.UTC().Format(time.RFC3339),
		Level:    r.Level.String(),
		Category: cat,
		Msg:      r.Message,
		Attrs:    attrsStr,
	})
	return h.inner.Handle(ctx, r)
}

func (h *BufferedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &BufferedHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *BufferedHandler) WithGroup(name string) slog.Handler {
	return &BufferedHandler{inner: h.inner.WithGroup(name)}
}
