import { useEffect, useMemo, useRef, useState } from 'react'
import { Trash2, WifiOff, Wifi } from 'lucide-react'
import { useAuthStore } from '../store/authStore'
import { useLangStore } from '../store/langStore'
import { useT } from '../i18n'

interface LogEntry {
  time: string
  level: string
  category: string
  msg: string
  attrs?: string
}

const LEVEL_STYLES: Record<string, { badge: string; row: string }> = {
  INFO:  { badge: 'bg-green-900/60 text-green-300',  row: '' },
  WARN:  { badge: 'bg-yellow-900/60 text-yellow-300', row: 'bg-yellow-900/10' },
  ERROR: { badge: 'bg-red-900/60 text-red-300',      row: 'bg-red-900/10' },
  DEBUG: { badge: 'bg-gray-700 text-gray-400',        row: '' },
}

const ALL_LEVELS = ['INFO', 'WARN', 'ERROR', 'DEBUG']

// 分类标签页（与后端 category 一致）。'all' 为聚合视图。
const CATEGORIES = ['all', 'http', 'vowifi', 'poller', 'scheduler', 'telegram', 'system'] as const
type Category = typeof CATEGORIES[number]

const CAT_LABELS: Record<Category, { zh: string; en: string }> = {
  all:       { zh: '全部',    en: 'All' },
  http:      { zh: 'HTTP 请求', en: 'HTTP' },
  vowifi:    { zh: 'VoWiFi',  en: 'VoWiFi' },
  poller:    { zh: '设备轮询', en: 'Poller' },
  scheduler: { zh: '定时任务', en: 'Scheduler' },
  telegram:  { zh: 'Telegram', en: 'Telegram' },
  system:    { zh: '系统',     en: 'System' },
}

// 每个分类标签页对应的强调色（选中态）。
const CAT_ACTIVE: Record<Category, string> = {
  all:       'border-blue-500 text-blue-400 bg-blue-900/20',
  http:      'border-sky-500 text-sky-400 bg-sky-900/20',
  vowifi:    'border-emerald-500 text-emerald-400 bg-emerald-900/20',
  poller:    'border-amber-500 text-amber-400 bg-amber-900/20',
  scheduler: 'border-violet-500 text-violet-400 bg-violet-900/20',
  telegram:  'border-cyan-500 text-cyan-400 bg-cyan-900/20',
  system:    'border-gray-400 text-gray-300 bg-gray-700/40',
}

export default function Logs() {
  const t = useT()
  const lang = useLangStore(s => s.lang)
  const token = useAuthStore(s => s.token)
  const [entries, setEntries] = useState<LogEntry[]>([])
  const [connected, setConnected] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const [cat, setCat] = useState<Category>('all')
  const [hiddenLevels, setHiddenLevels] = useState<Set<string>>(new Set())
  const [keyword, setKeyword] = useState('')
  const bottomRef = useRef<HTMLDivElement>(null)
  const wsRef = useRef<WebSocket | null>(null)

  useEffect(() => {
    if (!token) return
    let closed = false
    let ws: WebSocket | null = null
    let retry: ReturnType<typeof setTimeout> | undefined
    const connect = () => {
      const protocol = location.protocol === 'https:' ? 'wss' : 'ws'
      ws = new WebSocket(`${protocol}://${location.host}/ws/logs?token=${token}`)
      wsRef.current = ws
      ws.onopen = () => setConnected(true)
      ws.onclose = () => {
        setConnected(false)
        if (!closed) retry = setTimeout(connect, 3000)
      }
      ws.onerror = () => ws?.close()
      ws.onmessage = (e) => {
        try {
          const entry: LogEntry = JSON.parse(e.data)
          setEntries(prev => {
            const next = [...prev, entry]
            return next.length > 3000 ? next.slice(-3000) : next
          })
        } catch { /* ignore malformed */ }
      }
    }
    connect()
    return () => { closed = true; clearTimeout(retry); ws?.close() }
  }, [token])

  useEffect(() => {
    if (autoScroll) bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [entries, autoScroll, cat, hiddenLevels, keyword])

  // 各分类的条数（用于标签页角标）。
  const catCounts = useMemo(() => {
    const c: Record<string, number> = {}
    for (const e of entries) c[e.category || 'system'] = (c[e.category || 'system'] || 0) + 1
    c.all = entries.length
    return c
  }, [entries])

  const filtered = entries.filter(e => {
    if (cat !== 'all' && (e.category || 'system') !== cat) return false
    const lv = (e.level || '').toUpperCase()
    if (hiddenLevels.has(lv)) return false
    if (keyword) {
      const kw = keyword.toLowerCase()
      return e.msg.toLowerCase().includes(kw) || (e.attrs || '').toLowerCase().includes(kw)
    }
    return true
  })

  const toggleLevel = (lv: string) => {
    setHiddenLevels(prev => {
      const next = new Set(prev)
      next.has(lv) ? next.delete(lv) : next.add(lv)
      return next
    })
  }

  const catLabel = (c: Category) => CAT_LABELS[c][lang === 'zh' ? 'zh' : 'en']

  return (
    <div className="flex flex-col h-full min-h-0 gap-3">
      {/* Header */}
      <div className="flex items-center gap-3 flex-wrap">
        <h1 className="text-xl font-semibold">{t('nav_logs')}</h1>
        <span className={`flex items-center gap-1.5 text-xs px-2 py-0.5 rounded-full ${connected ? 'bg-green-900/40 text-green-400' : 'bg-gray-700 text-gray-400'}`}>
          {connected ? <Wifi className="w-3 h-3" /> : <WifiOff className="w-3 h-3" />}
          {connected ? t('logs_connected') : t('logs_disconnected')}
        </span>
        <span className="text-xs text-[var(--text-secondary)]">{filtered.length} {t('logs_lines')}</span>
        <div className="ml-auto flex items-center gap-2">
          <button
            onClick={() => setAutoScroll(v => !v)}
            className={`text-xs px-3 py-1 rounded border transition-colors ${autoScroll ? 'border-blue-500 text-blue-400 bg-blue-900/20' : 'border-[var(--border)] text-[var(--text-secondary)]'}`}
          >
            {t('logs_auto_scroll')}
          </button>
          <button
            onClick={() => setEntries([])}
            className="flex items-center gap-1 text-xs px-3 py-1 rounded border border-[var(--border)] text-[var(--text-secondary)] hover:text-red-400 hover:border-red-500 transition-colors"
          >
            <Trash2 className="w-3 h-3" />
            {t('logs_clear')}
          </button>
        </div>
      </div>

      {/* Category tabs */}
      <div className="flex items-center gap-1.5 flex-wrap border-b border-[var(--border)] pb-2">
        {CATEGORIES.map(c => {
          const active = cat === c
          const n = catCounts[c] || 0
          return (
            <button
              key={c}
              onClick={() => setCat(c)}
              className={`text-xs px-3 py-1.5 rounded-full border transition-colors ${active ? CAT_ACTIVE[c] : 'border-transparent text-[var(--text-secondary)] hover:bg-white/5'}`}
            >
              {catLabel(c)}
              {n > 0 && <span className={`ml-1.5 px-1.5 rounded-full text-[10px] ${active ? 'bg-white/15' : 'bg-white/10'}`}>{n}</span>}
            </button>
          )
        })}
      </div>

      {/* Filter bar: keyword + level */}
      <div className="flex items-center gap-2 flex-wrap">
        <input
          value={keyword}
          onChange={e => setKeyword(e.target.value)}
          placeholder={t('logs_search')}
          className="text-sm px-3 py-1.5 rounded border border-[var(--border)] bg-[var(--bg-secondary)] text-[var(--text-primary)] outline-none focus:border-blue-500 w-56"
        />
        <span className="text-xs text-[var(--text-secondary)] ml-1">{t('logs_level')}:</span>
        {ALL_LEVELS.map(lv => {
          const active = !hiddenLevels.has(lv)
          const s = LEVEL_STYLES[lv]
          return (
            <button
              key={lv}
              onClick={() => toggleLevel(lv)}
              className={`text-xs px-3 py-1 rounded-full border transition-colors ${active ? s.badge + ' border-transparent' : 'border-[var(--border)] text-[var(--text-secondary)] opacity-50'}`}
            >
              {lv}
            </button>
          )
        })}
      </div>

      {/* Log table */}
      <div className="flex-1 min-h-0 overflow-y-auto rounded-lg border border-[var(--border)] bg-[var(--bg-secondary)] font-mono text-xs">
        {filtered.length === 0 ? (
          <div className="flex items-center justify-center h-40 text-[var(--text-secondary)]">
            {connected ? t('logs_empty') : t('logs_connecting')}
          </div>
        ) : (
          <div className="overflow-x-auto"><table className="w-full min-w-[720px] border-collapse">
            <tbody>
              {filtered.map((e, i) => {
                const lv = (e.level || '').toUpperCase()
                const s = LEVEL_STYLES[lv] || LEVEL_STYLES.INFO
                return (
                  <tr key={i} className={`border-b border-[var(--border)]/30 hover:bg-white/5 ${s.row}`}>
                    <td className="px-3 py-1 whitespace-nowrap text-[var(--text-secondary)] w-40">
                      {e.time?.replace('T', ' ').replace('Z', '')}
                    </td>
                    <td className="px-2 py-1 w-14">
                      <span className={`px-1.5 py-0.5 rounded text-[10px] font-semibold ${s.badge}`}>{lv}</span>
                    </td>
                    {cat === 'all' && (
                      <td className="px-2 py-1 w-20">
                        <span className="px-1.5 py-0.5 rounded text-[10px] bg-white/10 text-[var(--text-secondary)]">{e.category || 'system'}</span>
                      </td>
                    )}
                    <td className="px-3 py-1 text-[var(--text-primary)] break-all">
                      {e.msg}
                      {e.attrs && <span className="ml-2 text-[var(--text-secondary)]">{e.attrs}</span>}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table></div>
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  )
}
