import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import clsx from 'clsx'
import { useAuthStore } from '../store/authStore'
import { Send, Trash2, RefreshCw, Bot, Paperclip, X } from 'lucide-react'
import { format, isToday, isYesterday } from 'date-fns'

// iMessage 风格居中日期分隔（与消息中心一致）
const TG_WEEK = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']
function tgDaySep(iso: string): { date: string; time: string } {
  const d = new Date(iso)
  const time = format(d, 'HH:mm')
  if (isToday(d)) return { date: '今天', time }
  if (isYesterday(d)) return { date: '昨天', time }
  return { date: `${format(d, 'M月d日')} ${TG_WEEK[d.getDay()]}`, time }
}
import {
  getTelegramMessagesApi, sendTelegramMessageApi, clearTelegramMessagesApi,
  getTelegramConfigApi, sendTelegramFileApi, type TelegramMessage, type TelegramConfig,
} from '../api/telegram'

function StatusDot({ ok }: { ok: boolean }) {
  return (
    <span className={`inline-block w-2 h-2 rounded-full ${ok ? 'bg-green-400' : 'bg-red-400'}`} />
  )
}

export default function TelegramAdmin() {
  const token = useAuthStore(s => s.token)
  const fileUrl = (fileId: string) => `/api/v1/telegram/file/${fileId}?token=${token}`
  const [lightbox, setLightbox] = useState<string | null>(null)
  const [messages, setMessages] = useState<TelegramMessage[]>([])
  const [config, setConfig] = useState<TelegramConfig | null>(null)
  const [input, setInput] = useState('')
  const [pendingFile, setPendingFile] = useState<File | null>(null)
  const [sending, setSending] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [loading, setLoading] = useState(true)
  const [clearing, setClearing] = useState(false)
  const [autoRefresh, setAutoRefresh] = useState(true)
  const bottomRef = useRef<HTMLDivElement>(null)
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)

  async function load() {
    try {
      const [msgRes, cfgRes] = await Promise.all([
        getTelegramMessagesApi(0, 50),
        getTelegramConfigApi(),
      ])
      setMessages(msgRes.data.slice().reverse())
      setConfig(cfgRes.data)
    } catch {
      // ignore
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
  }, [])

  useEffect(() => {
    if (autoRefresh) {
      timerRef.current = setInterval(load, 15000) // WS 已实时推送消息，轮询降为兜底 + 刷新 Bot 状态
    } else {
      if (timerRef.current) clearInterval(timerRef.current)
    }
    return () => { if (timerRef.current) clearInterval(timerRef.current) }
  }, [autoRefresh])

  // WebSocket 实时接收新的 Telegram 消息（收/发），无需等 5 秒轮询
  useEffect(() => {
    if (!token) return
    let ws: WebSocket | null = null
    let closed = false
    const connect = () => {
      const protocol = location.protocol === 'https:' ? 'wss' : 'ws'
      ws = new WebSocket(`${protocol}://${location.host}/ws/telegram?token=${token}`)
      ws.onmessage = e => {
        try {
          const m = JSON.parse(e.data) as TelegramMessage
          setMessages(prev => {
            const i = prev.findIndex(x => x.id === m.id)
            if (i >= 0) { const c = [...prev]; c[i] = { ...c[i], ...m }; return c }
            return [...prev, m] // 新消息追加到末尾（升序，最新在底部）
          })
        } catch { /* ignore */ }
      }
      ws.onclose = () => { if (!closed) setTimeout(connect, 3000) }
    }
    connect()
    return () => { closed = true; ws?.close() }
  }, [token])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  async function handleSend() {
    if (!input.trim() && !pendingFile) return
    setSending(true)
    try {
      if (pendingFile) {
        await sendTelegramFileApi(pendingFile, input.trim() || undefined)
        setPendingFile(null)
        setInput('')
      } else {
        await sendTelegramMessageApi(input.trim())
        setInput('')
      }
      await load()
    } catch {
      alert('发送失败')
    } finally {
      setSending(false)
    }
  }

  async function handleClear() {
    if (!confirm('确认清空所有 Telegram 消息记录？')) return
    setClearing(true)
    try {
      await clearTelegramMessagesApi()
      setMessages([])
    } finally {
      setClearing(false)
    }
  }

  const renderText = (text: string) =>
    text.replace(/<[^>]+>/g, '')

  function DocMedia({ fileId, fileUrl, onPreview }: { fileId: string; fileUrl: (id: string) => string; onPreview: (url: string) => void }) {
    const [failed, setFailed] = useState(false)
    if (failed) return <a href={fileUrl(fileId)} target="_blank" rel="noreferrer" className="underline text-blue-300 text-sm">⬇ 下载文件</a>
    return (
      <img
        src={fileUrl(fileId)}
        alt="文件"
        className="max-w-[240px] rounded-xl cursor-zoom-in"
        onClick={() => onPreview(fileUrl(fileId))}
        onError={() => setFailed(true)}
      />
    )
  }

  function PhotoMedia({ fileId, isSticker }: { fileId: string; isSticker: boolean }) {
    const [failed, setFailed] = useState(false)
    if (failed) return null
    return (
      <img
        src={fileUrl(fileId)}
        alt="图片"
        className={`rounded-xl cursor-zoom-in ${isSticker ? 'w-24 h-24' : 'max-w-[240px]'}`}
        onClick={() => setLightbox(fileUrl(fileId))}
        onError={() => setFailed(true)}
      />
    )
  }

  function MediaContent({ msg }: { msg: TelegramMessage }) {
    if ((msg.file_type === 'photo' || msg.file_type === 'sticker') && msg.file_id) {
      return <PhotoMedia fileId={msg.file_id} isSticker={msg.file_type === 'sticker'} />
    }
    if (msg.file_type === 'document' && msg.file_id) {
      return (
        <DocMedia fileId={msg.file_id} fileUrl={fileUrl} onPreview={setLightbox} />
      )
    }
    if (msg.file_type === 'video' && msg.file_id) {
      return (
        <video
          src={fileUrl(msg.file_id)}
          controls
          className="max-w-[240px] rounded-xl"
        />
      )
    }
    if (msg.file_type === 'voice' && msg.file_id) {
      return (
        <audio src={fileUrl(msg.file_id)} controls className="w-48" />
      )
    }
    return null
  }

  return (
    <div className="flex flex-col h-full gap-4">
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 min-w-0">
          <Bot className="w-6 h-6 text-blue-400 shrink-0" />
          <h1 className="text-xl font-bold text-[var(--text-primary)]">Telegram 管理</h1>
          {config && (
            <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-sm text-[var(--text-secondary)]">
              <StatusDot ok={config.bot_token_set} />
              <span>Bot {config.bot_token_set ? '已连接' : '未配置'}</span>
              <span className="text-[var(--border)]">·</span>
              <StatusDot ok={config.polling} />
              <span>{config.polling ? '轮询中' : '未运行'}</span>
              {config.chat_id && (
                <>
                  <span className="text-[var(--border)]">·</span>
                  <span>Chat: {config.chat_id}</span>
                </>
              )}
            </div>
          )}
        </div>
        <div className="flex items-center gap-2 shrink-0 self-end sm:self-auto">
          <button
            onClick={() => setAutoRefresh(v => !v)}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm transition-colors ${
              autoRefresh
                ? 'bg-blue-500/20 text-blue-400'
                : 'bg-[var(--bg-card)] text-[var(--text-secondary)]'
            }`}
          >
            <RefreshCw className={`w-3.5 h-3.5 ${autoRefresh ? 'animate-spin' : ''}`} style={{ animationDuration: '3s' }} />
            自动刷新
          </button>
          <button
            onClick={load}
            className="p-2 rounded-lg hover:bg-[var(--bg-card)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors"
            title="刷新"
          >
            <RefreshCw className="w-4 h-4" />
          </button>
          <button
            onClick={handleClear}
            disabled={clearing}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm bg-red-500/10 text-red-400 hover:bg-red-500/20 transition-colors disabled:opacity-50"
          >
            <Trash2 className="w-3.5 h-3.5" />
            清空记录
          </button>
        </div>
      </div>

      {/* Chat window */}
      <div className="flex-1 bg-[var(--bg-card)] border border-[var(--border)] rounded-2xl flex flex-col overflow-hidden min-h-0">
        <div className="flex-1 overflow-y-auto p-4 space-y-3">
          {loading ? (
            <div className="text-center text-[var(--text-secondary)] py-12">加载中…</div>
          ) : messages.length === 0 ? (
            <div className="text-center text-[var(--text-secondary)] py-12">暂无消息记录</div>
          ) : (
            messages.map((msg, i) => {
              const out = msg.direction === 'out'
              const showTime = i === 0 || (+new Date(msg.created_at) - +new Date(messages[i - 1].created_at)) > 5 * 60 * 1000
              return (
                <div key={msg.id}>
                  {showTime && (
                    <div className="text-center text-[11px] text-gray-500 my-3">
                      <span className="font-semibold text-gray-400">{tgDaySep(msg.created_at).date}</span> {tgDaySep(msg.created_at).time}
                    </div>
                  )}
                  <div className={clsx('flex', out ? 'justify-end' : 'justify-start')}>
                    <div className={clsx('max-w-[75%] rounded-2xl px-3.5 py-2 text-sm whitespace-pre-wrap break-words',
                      out ? 'bg-blue-600 text-white' : 'bg-gray-700 text-gray-100')}>
                      <MediaContent msg={msg} />
                      {msg.file_type && msg.text && msg.text !== '[图片]' && msg.text !== '[视频]' && msg.text !== '[贴纸]' && msg.text !== '[语音]' && (
                        <div className="mt-1 text-xs opacity-80">{renderText(msg.text)}</div>
                      )}
                      {!msg.file_type && renderText(msg.text)}
                      <div className={clsx('flex items-center gap-1.5 mt-1 text-[10px]', out ? 'text-blue-200/70 justify-end' : 'text-gray-400')}>
                        {msg.direction === 'in' && <span className="font-medium">{msg.username || msg.chat_id}</span>}
                        <span>{format(new Date(msg.created_at), 'HH:mm')}</span>
                        {msg.is_command && <span className="bg-purple-500/20 text-purple-400 px-1.5 py-0.5 rounded text-[9px]">命令</span>}
                      </div>
                    </div>
                  </div>
                </div>
              )
            })
          )}
          <div ref={bottomRef} />
        </div>

        {/* Input — 对齐消息中心：无顶部分隔线、圆形发光发送钮 */}
        <div className="p-3 flex flex-col gap-2">
          {/* File preview */}
          {pendingFile && (
            <div className="flex items-center gap-2 px-3 py-2 bg-blue-500/10 border border-blue-500/30 rounded-xl text-sm">
              {pendingFile.type.startsWith('image/') ? (
                <img src={URL.createObjectURL(pendingFile)} alt="" className="h-12 w-12 object-cover rounded-lg" />
              ) : (
                <Paperclip className="w-4 h-4 text-blue-400" />
              )}
              <span className="flex-1 text-[var(--text-primary)] truncate">{pendingFile.name}</span>
              <button onClick={() => setPendingFile(null)} className="text-[var(--text-secondary)] hover:text-red-400">
                <X className="w-4 h-4" />
              </button>
            </div>
          )}
          <div className="flex items-end gap-2">
            <input
              ref={fileInputRef}
              type="file"
              className="hidden"
              accept="image/*,video/*,audio/*,.pdf,.doc,.docx,.xls,.xlsx,.zip,.rar"
              onChange={e => { if (e.target.files?.[0]) setPendingFile(e.target.files[0]); e.target.value = '' }}
            />
            <button
              onClick={() => fileInputRef.current?.click()}
              className="shrink-0 w-9 h-9 flex items-center justify-center text-gray-400 hover:text-blue-400 rounded-full hover:bg-gray-700/50 transition-colors"
              title="发送图片/文件"
            >
              <Paperclip className="w-4 h-4" />
            </button>
            <textarea
              value={input}
              onChange={e => setInput(e.target.value)}
              onKeyDown={e => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault()
                  handleSend()
                }
              }}
              placeholder={pendingFile ? '添加说明文字（可选）…' : '输入消息发送到 Telegram…'}
              rows={1}
              className="flex-1 resize-none bg-gray-800 border border-gray-600 rounded-xl px-3.5 py-2 text-sm text-white placeholder-gray-500 focus:outline-none focus:border-blue-500 max-h-32"
            />
            <button
              onClick={handleSend}
              disabled={sending || (!input.trim() && !pendingFile)}
              className="shrink-0 w-9 h-9 flex items-center justify-center rounded-full bg-blue-500 hover:bg-blue-400 text-white shadow-lg shadow-blue-500/50 disabled:opacity-70 transition-all"
            >
              <Send className="w-4 h-4" />
            </button>
          </div>
        </div>
      </div>

      {/* Stats bar */}
      <div className="text-xs text-[var(--text-secondary)] flex gap-4">
        <span>共 {messages.length} 条消息</span>
        <span>收到: {messages.filter(m => m.direction === 'in').length}</span>
        <span>发出: {messages.filter(m => m.direction === 'out').length}</span>
        <span>命令: {messages.filter(m => m.is_command).length}</span>
      </div>

      {/* Lightbox — portal 到 body 避免祖先 transform 导致 fixed 失效 */}
      {lightbox && createPortal(
        <div
          className="fixed inset-0 z-50 bg-black/80 flex items-center justify-center cursor-zoom-out"
          onClick={() => setLightbox(null)}
        >
          <img
            src={lightbox}
            alt="预览"
            className="max-w-[90vw] max-h-[90vh] rounded-xl object-contain shadow-2xl"
            onClick={e => e.stopPropagation()}
          />
          <button
            className="absolute top-4 right-6 text-white text-3xl font-light hover:text-gray-300"
            onClick={() => setLightbox(null)}
          >✕</button>
        </div>,
        document.body
      )}
    </div>
  )
}
