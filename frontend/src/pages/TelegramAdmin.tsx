import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useAuthStore } from '../store/authStore'
import { Send, Trash2, RefreshCw, Bot, Paperclip, X } from 'lucide-react'
import { format } from 'date-fns'
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
      timerRef.current = setInterval(load, 5000)
    } else {
      if (timerRef.current) clearInterval(timerRef.current)
    }
    return () => { if (timerRef.current) clearInterval(timerRef.current) }
  }, [autoRefresh])

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
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Bot className="w-6 h-6 text-blue-400" />
          <h1 className="text-xl font-bold text-[var(--text-primary)]">Telegram 管理</h1>
          {config && (
            <div className="flex items-center gap-2 text-sm text-[var(--text-secondary)]">
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
        <div className="flex items-center gap-2">
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
            messages.map(msg => (
              <div key={msg.id} className={`flex ${msg.direction === 'out' ? 'justify-end' : 'justify-start'}`}>
                <div className={`max-w-[70%] ${msg.direction === 'out' ? 'items-end' : 'items-start'} flex flex-col gap-0.5`}>
                  <div className="flex items-center gap-1.5 text-xs text-[var(--text-secondary)]">
                    {msg.direction === 'in' && (
                      <span className="font-medium">{msg.username || msg.chat_id}</span>
                    )}
                    <span>{format(new Date(msg.created_at), 'MM-dd HH:mm:ss')}</span>
                    {msg.is_command && (
                      <span className="bg-purple-500/20 text-purple-400 px-1.5 py-0.5 rounded text-[10px]">命令</span>
                    )}
                  </div>
                  <div className={`px-3 py-2 rounded-2xl text-sm whitespace-pre-wrap break-words ${
                    msg.direction === 'out'
                      ? 'bg-blue-600 text-white rounded-br-sm'
                      : 'bg-[var(--bg-main)] text-[var(--text-primary)] border border-[var(--border)] rounded-bl-sm'
                  }`}>
                    <MediaContent msg={msg} />
                    {msg.file_type && msg.text && msg.text !== '[图片]' && msg.text !== '[视频]' && msg.text !== '[贴纸]' && msg.text !== '[语音]' && (
                      <div className="mt-1 text-xs opacity-80">{renderText(msg.text)}</div>
                    )}
                    {!msg.file_type && renderText(msg.text)}
                  </div>
                </div>
              </div>
            ))
          )}
          <div ref={bottomRef} />
        </div>

        {/* Input */}
        <div className="border-t border-[var(--border)] p-3 flex flex-col gap-2">
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
          <div className="flex gap-2">
            <input
              ref={fileInputRef}
              type="file"
              className="hidden"
              accept="image/*,video/*,audio/*,.pdf,.doc,.docx,.xls,.xlsx,.zip,.rar"
              onChange={e => { if (e.target.files?.[0]) setPendingFile(e.target.files[0]); e.target.value = '' }}
            />
            <button
              onClick={() => fileInputRef.current?.click()}
              className="self-end p-2 rounded-xl border border-[var(--border)] text-[var(--text-secondary)] hover:text-blue-400 hover:border-blue-400 transition-colors"
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
              placeholder={pendingFile ? "添加说明文字（可选）…" : "输入消息发送到 Telegram… (Enter 发送，Shift+Enter 换行)"}
              rows={2}
              className="flex-1 bg-[var(--bg-main)] border border-[var(--border)] rounded-xl px-3 py-2 text-sm text-[var(--text-primary)] placeholder-[var(--text-secondary)] resize-none focus:outline-none focus:ring-2 focus:ring-blue-500/50"
            />
            <button
              onClick={handleSend}
              disabled={sending || (!input.trim() && !pendingFile)}
              className="self-end px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-40 text-white rounded-xl text-sm font-medium transition-colors flex items-center gap-1.5"
            >
              <Send className="w-4 h-4" />
              发送
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
