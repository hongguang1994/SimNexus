import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { format, isToday, isYesterday } from 'date-fns'
import { Send, Search, MessageSquare, Wifi, Radio, RefreshCw, PenSquare, X, Loader2, ChevronLeft, Clock } from 'lucide-react'
import clsx from 'clsx'
import { getMessagesApi, sendSmsApi, SmsMessage, getTasksApi, createTaskApi, deleteTaskApi, ScheduledTask } from '../api/sms'
import { getContactsApi, Contact } from '../api/contacts'
import { useModemStore } from '../store/modemStore'
import { useAuthStore } from '../store/authStore'
import { useT } from '../i18n'

// 会话时间：今天显示 HH:mm，昨天显示“昨天”，更早显示 MM/dd
function convTime(iso: string): string {
  const d = new Date(iso)
  if (isToday(d)) return format(d, 'HH:mm')
  if (isYesterday(d)) return '昨天'
  return format(d, 'MM/dd')
}

// iMessage 风格的居中日期分隔：日期加粗 + 时间常规。今天/昨天用中文标签（与 convTime 一致），
// 更早显示「M月d日 周X」。
const WEEK = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']
function daySep(iso: string): { date: string; time: string } {
  const d = new Date(iso)
  const time = format(d, 'HH:mm')
  if (isToday(d)) return { date: '今天', time }
  if (isYesterday(d)) return { date: '昨天', time }
  return { date: `${format(d, 'M月d日')} ${WEEK[d.getDay()]}`, time }
}

// 头像用号码后两位 / 首字符，配一个稳定的底色
const AVATAR_COLORS = ['bg-blue-500', 'bg-emerald-500', 'bg-violet-500', 'bg-amber-500', 'bg-rose-500', 'bg-cyan-500', 'bg-indigo-500']
// 卡标签配色（区分不同 SIM 卡），按卡 id 取模稳定映射
const CARD_TINTS = [
  'bg-sky-500/15 text-sky-300 border-sky-500/30',
  'bg-emerald-500/15 text-emerald-300 border-emerald-500/30',
  'bg-violet-500/15 text-violet-300 border-violet-500/30',
  'bg-amber-500/15 text-amber-300 border-amber-500/30',
  'bg-rose-500/15 text-rose-300 border-rose-500/30',
  'bg-cyan-500/15 text-cyan-300 border-cyan-500/30',
]

function avatarFor(number: string) {
  const digits = number.replace(/\D/g, '')
  const label = digits.slice(-2) || number.slice(0, 2) || '?'
  let h = 0
  for (const c of number) h = (h * 31 + c.charCodeAt(0)) & 0xffff
  return { label, color: AVATAR_COLORS[h % AVATAR_COLORS.length] }
}

export default function MessageCenter() {
  const modems = useModemStore(s => s.modems)
  const t = useT()
  const [messages, setMessages] = useState<SmsMessage[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(true)
  const [query, setQuery] = useState('')
  const [cardFilter, setCardFilter] = useState<number | 'all'>('all') // 顶部卡片筛选：'all' 或某卡 id
  const [isMobile, setIsMobile] = useState(false)
  useEffect(() => {
    const mq = window.matchMedia('(max-width: 767px)')
    const on = () => setIsMobile(mq.matches)
    on()
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  const threadRef = useRef<HTMLDivElement>(null)
  const location = useLocation()
  const navigate = useNavigate()
  // 新建消息（类 Apple 信息）：选卡 + 输入号码 + 发送
  const [composing, setComposing] = useState(false)
  const [composeCard, setComposeCard] = useState<number | ''>('')
  const [composeTo, setComposeTo] = useState('')
  const [composeText, setComposeText] = useState('')
  // 定时发送：待发的单次任务（send_once_at 且仍 active），内联为会话里的“待发”气泡
  const [tasks, setTasks] = useState<ScheduledTask[]>([])
  const [scheduleCtx, setScheduleCtx] = useState<null | 'reply' | 'compose'>(null) // 哪个发送栏在定时
  const [scheduleAt, setScheduleAt] = useState('') // datetime-local 值
  const [scheduling, setScheduling] = useState(false)
  // 通讯录：号码命中则显示联系人姓名（类 iMessage）
  const [contacts, setContacts] = useState<Contact[]>([])
  useEffect(() => { getContactsApi().then(r => setContacts(r.data)).catch(() => {}) }, [])
  const contactByPhone = useMemo(() => {
    const m = new Map<string, string>()
    for (const c of contacts) {
      const n = (c.phone || '').replace(/\D/g, '')
      if (n && c.name.trim()) m.set(n, c.name.trim())
    }
    return m
  }, [contacts])
  // 号码 → 显示名：优先精确匹配（去掉非数字），退化到后 8 位后缀匹配（容忍国家码差异）
  const displayName = (number: string): string => {
    const n = (number || '').replace(/\D/g, '')
    if (!n) return number
    const hit = contactByPhone.get(n)
    if (hit) return hit
    if (n.length >= 8) {
      const suf = n.slice(-8)
      for (const [k, name] of contactByPhone) if (k.endsWith(suf)) return name
    }
    return number
  }
  // 头像：命中联系人时用姓名首字，否则用号码后两位
  const avatarOf = (number: string) => {
    const dn = displayName(number)
    const av = avatarFor(number)
    return dn === number ? av : { label: dn.trim()[0]?.toUpperCase() || av.label, color: av.color }
  }

  const loadTasks = async () => {
    try { setTasks((await getTasksApi()).data) } catch { /* ignore */ }
  }

  const load = async () => {
    loadTasks()
    try {
      const r = await getMessagesApi({ limit: 1000 })
      // 保留尚未落库的“发送中”乐观气泡（临时负 id），直到服务端出现等价记录再让其消失，
      // 避免 3s 兜底轮询把还在发送的 VoWiFi 气泡刷掉造成闪烁
      setMessages(prev => {
        const pending = prev.filter(m => m.id < 0 && !r.data.some(
          s => s.direction === m.direction && s.phone_number === m.phone_number && s.content === m.content))
        return [...r.data, ...pending]
      })
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    load()
    const timer = setInterval(load, 3000) // 兜底轮询（WS 漏收时补齐）
    return () => clearInterval(timer)
  }, [])

  // 从通讯录「发送信息」跳来：预填号码并打开新建消息
  useEffect(() => {
    const to = (location.state as any)?.composeTo
    if (!to) return
    setComposing(true); setSelected(null); setComposeTo(String(to)); setComposeText('')
    const first = modems.find(m => m.status === 'connected' || m.status === 'disconnected')
    if (first) setComposeCard(prev => (prev === '' ? first.id : prev))
    navigate('.', { replace: true, state: null }) // 清掉 state，避免返回/刷新重复触发
  }, [location.state]) // eslint-disable-line

  // WebSocket 实时接收新短信（MT 收 / MO 发），无需等轮询
  const token = useAuthStore(s => s.token)
  useEffect(() => {
    if (!token) return
    let ws: WebSocket | null = null
    let closed = false
    const connect = () => {
      const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws'
      ws = new WebSocket(`${protocol}://${window.location.host}/ws/messages?token=${token}`)
      ws.onmessage = e => {
        try {
          const m = JSON.parse(e.data) as SmsMessage
          setMessages(prev => {
            const i = prev.findIndex(x => x.id === m.id)
            if (i >= 0) {
              const c = [...prev]
              c[i] = { ...c[i], ...m }
              return c
            }
            // 丢弃与之等价的“发送中”乐观气泡，用真实记录替代，避免重复
            const cleaned = prev.filter(x => !(x.id < 0 && x.direction === m.direction
              && x.phone_number === m.phone_number && x.content === m.content))
            return [...cleaned, m]
          })
        } catch {
          /* ignore */
        }
      }
      ws.onclose = () => { if (!closed) setTimeout(connect, 3000) }
    }
    connect()
    return () => { closed = true; ws?.close() }
  }, [token])

  // 待发的定时消息：单次(send_once_at)且仍 active 的任务，按 (卡+每个收件人) 归到对应会话。
  const scheduledByKey = useMemo(() => {
    const map = new Map<string, ScheduledTask[]>()
    for (const tk of tasks) {
      if (tk.status !== 'active' || !tk.send_once_at) continue
      for (const r of tk.recipients || []) {
        const k = `${tk.modem_id}|${r}`
        if (!map.has(k)) map.set(k, [])
        map.get(k)!.push(tk)
      }
    }
    for (const arr of map.values()) arr.sort((a, b) => +new Date(a.send_once_at!) - +new Date(b.send_once_at!))
    return map
  }, [tasks])

  // 按 (卡 + 对端号码) 分组成会话——系统管理多张卡，不同卡发给同一号码是不同的会话；
  // 会话 key = `${modemId}|${number}`，按最新活动时间倒序。同时并入“只有定时待发、还没消息”的会话。
  const conversations = useMemo(() => {
    const map = new Map<string, SmsMessage[]>()
    for (const m of messages) {
      const k = `${m.modem_id}|${m.phone_number || t('unknown')}`
      if (!map.has(k)) map.set(k, [])
      map.get(k)!.push(m)
    }
    // 并入仅有定时待发的会话 key（没有历史消息也要出现在列表里）
    for (const k of scheduledByKey.keys()) if (!map.has(k)) map.set(k, [])

    const list = [...map.entries()].map(([key, msgs]) => {
      msgs.sort((a, b) => +new Date(a.created_at) - +new Date(b.created_at))
      const last = msgs[msgs.length - 1]
      const scheduled = scheduledByKey.get(key) || []
      const [mid, ...rest] = key.split('|')
      const number = last?.phone_number || rest.join('|') || t('unknown')
      const modemId = last?.modem_id ?? Number(mid)
      // 排序时间：有消息用最后消息时间；否则用最早的定时时间
      const sortAt = last?.created_at || scheduled[0]?.send_once_at || new Date(0).toISOString()
      return { key, number, modemId, msgs, last, scheduled, sortAt }
    })
    list.sort((a, b) => +new Date(b.sortAt) - +new Date(a.sortAt))
    return list
  }, [messages, scheduledByKey, t])

  // 会话里出现过的卡 id（用于顶部筛选器，只在有多张卡时才显示）
  const cardIds = useMemo(() => [...new Set(conversations.map(c => c.modemId))], [conversations])

  const filtered = useMemo(() => {
    let list = conversations
    if (cardFilter !== 'all') list = list.filter(c => c.modemId === cardFilter)
    if (query.trim()) {
      const q = query.trim().toLowerCase()
      list = list.filter(c => c.number.toLowerCase().includes(q) || displayName(c.number).toLowerCase().includes(q) || c.msgs.some(m => m.content.toLowerCase().includes(q)))
    }
    return list
  }, [conversations, query, cardFilter, contactByPhone])

  // 默认选中第一个会话（手机上不自动选，先展示列表，点了才进会话）
  useEffect(() => {
    if (isMobile) return
    if ((!selected || !filtered.some(c => c.key === selected)) && filtered.length) setSelected(filtered[0].key)
  }, [filtered, selected, isMobile])

  const current = conversations.find(c => c.key === selected)

  // 选中会话或有新消息时滚到底部
  useEffect(() => {
    if (threadRef.current) threadRef.current.scrollTop = threadRef.current.scrollHeight
  }, [selected, current?.msgs.length])

  const modemName = (id: number) => {
    const m = modems.find(x => x.id === id)
    return m?.alias || m?.operator || `SIM ${id}`
  }
  // 卡标签底色：按卡 id 稳定取色，用于会话列表/头部区分不同卡
  const cardTint = (id: number) => CARD_TINTS[id % CARD_TINTS.length]

  // 乐观发送：立即插入“发送中”气泡（VoWiFi 发送要做完整重注册，约几秒），API 返回后替换为真实
  // 状态。后端 WS 也会推同一条（真实 id），届时按 id 就地更新，不重复。
  const doSend = async (modemId: number, phone: string, content: string) => {
    const temp: SmsMessage = {
      id: -Date.now(), modem_id: modemId, direction: 'outbound', channel: 'vowifi',
      phone_number: phone, content, status: 'pending',
      error_message: null, sent_at: null, received_at: null, created_at: new Date().toISOString(),
    }
    setMessages(prev => [...prev, temp])
    try {
      const res = await sendSmsApi({ modem_id: modemId, phone_number: phone, content })
      setMessages(prev => prev.map(m => (m.id === temp.id ? res.data : m)))
    } catch {
      setMessages(prev => prev.map(m => (m.id === temp.id ? { ...m, status: 'failed' } : m)))
    }
  }

  const reply = () => {
    if (!input.trim() || !current) return
    const text = input.trim()
    setInput('')
    doSend(current.modemId, current.number, text)
  }

  // 定时发送：把当前发送栏的内容排成一个单次任务（send_once_at 存 UTC）。
  const submitSchedule = async () => {
    if (!scheduleAt) return
    let modemId = 0, to = '', text = ''
    if (scheduleCtx === 'reply' && current) { modemId = current.modemId; to = current.number; text = input.trim() }
    else if (scheduleCtx === 'compose') { modemId = Number(composeCard); to = composeTo.replace(/\s+/g, ''); text = composeText.trim() }
    if (!modemId || !to || !text) return
    setScheduling(true)
    try {
      await createTaskApi({
        name: `${t('nav_history')} · ${to}`,
        modem_id: modemId,
        recipients: [to],
        content: text,
        send_once_at: new Date(scheduleAt).toISOString(), // datetime-local(本地) → UTC ISO
      })
      await loadTasks()
      if (scheduleCtx === 'reply') setInput('')
      else { setComposeText(''); setComposing(false); setSelected(`${modemId}|${to}`) }
      setScheduleCtx(null); setScheduleAt('')
    } catch (e: any) {
      alert(e?.response?.data?.detail || e?.response?.data?.msg || t('sms_fail_default'))
    } finally { setScheduling(false) }
  }

  // 取消一条待发的定时消息（删任务）。
  const cancelScheduled = async (id: number) => {
    try { await deleteTaskApi(id); await loadTasks() } catch { /* ignore */ }
  }

  // datetime-local 的最小值 = 现在（本地时区），禁止选过去时间。
  const nowLocal = () => {
    const d = new Date(Date.now() - new Date().getTimezoneOffset() * 60000)
    return d.toISOString().slice(0, 16)
  }
  // 打开定时时的默认时间：一小时后（本地时区），避免面板出现时是空的。
  const defaultScheduleLocal = () => {
    const d = new Date(Date.now() + 60 * 60000 - new Date().getTimezoneOffset() * 60000)
    return d.toISOString().slice(0, 16)
  }
  const toggleSchedule = (ctx: 'reply' | 'compose') => {
    if (scheduleCtx === ctx) { setScheduleCtx(null); setScheduleAt('') }
    else { setScheduleCtx(ctx); if (!scheduleAt) setScheduleAt(defaultScheduleLocal()) }
  }
  const fmtSchedule = (iso: string) => format(new Date(iso), 'MM/dd HH:mm')

  // 可发送的卡：在线/离线均可选（排除异常状态），默认选第一张
  const sendableModems = modems.filter(m => m.status === 'connected' || m.status === 'disconnected')

  const startCompose = () => {
    setComposing(true)
    setSelected(null)
    setComposeTo('')
    setComposeText('')
    if (composeCard === '' && sendableModems[0]) setComposeCard(sendableModems[0].id)
  }

  const sendCompose = () => {
    const to = composeTo.replace(/\s+/g, '')
    if (!to || !composeText.trim() || composeCard === '') return
    const text = composeText.trim()
    doSend(Number(composeCard), to, text) // 乐观发送
    setComposeText('')
    setComposing(false)
    setSelected(`${Number(composeCard)}|${to}`) // 立即切到该(卡+号码)会话，能看到“发送中”气泡
  }

  // 手机上单栏：显示会话线程/新建时隐藏列表，否则显示列表
  const showThread = !!current || composing

  return (
    <div className="flex h-full rounded-2xl overflow-hidden border border-gray-700/60 bg-gray-900/40">
      {/* ── 左：会话列表（手机上进入会话后隐藏）── */}
      <aside className={clsx(
        'w-full md:w-72 md:shrink-0 border-r border-gray-700/60 flex-col min-h-0 bg-gray-800/30',
        isMobile && showThread ? 'hidden' : 'flex'
      )}>
        <div className="p-3 border-b border-gray-700/60 flex items-center gap-2">
          <h1 className="text-base font-bold text-white flex-1">{t('nav_history')}</h1>
          <button onClick={load} className="p-1.5 rounded-lg text-gray-400 hover:text-white hover:bg-gray-700/50" title={t('detail_refresh')}>
            <RefreshCw className={clsx('w-4 h-4', loading && 'animate-spin')} />
          </button>
          <button onClick={startCompose} className="p-1.5 rounded-lg text-blue-400 hover:text-blue-300 hover:bg-blue-500/10" title={t('msg_new')}>
            <PenSquare className="w-4 h-4" />
          </button>
        </div>
        <div className="p-2.5">
          <div className="flex items-center gap-2 bg-gray-900/60 rounded-lg px-2.5 py-1.5">
            <Search className="w-3.5 h-3.5 text-gray-500 shrink-0" />
            <input value={query} onChange={e => setQuery(e.target.value)} placeholder={t('msg_search')}
              className="bg-transparent text-sm text-white placeholder-gray-500 focus:outline-none w-full" />
          </div>
        </div>
        {/* 卡片筛选：多张卡时才显示，横向可滚动的一排卡片标签 */}
        {cardIds.length > 1 && (
          <div className="px-2.5 pb-2 flex items-center gap-1.5 overflow-x-auto">
            <button onClick={() => setCardFilter('all')}
              className={clsx('shrink-0 px-2.5 py-1 rounded-full text-[11px] border transition-colors',
                cardFilter === 'all' ? 'bg-blue-500/20 text-blue-200 border-blue-500/40' : 'text-gray-400 border-gray-700 hover:bg-gray-700/40')}>
              {t('msg_all_cards')}
            </button>
            {cardIds.map(id => (
              <button key={id} onClick={() => setCardFilter(id)}
                className={clsx('shrink-0 px-2.5 py-1 rounded-full text-[11px] border transition-colors whitespace-nowrap',
                  cardFilter === id ? cardTint(id) : 'text-gray-400 border-gray-700 hover:bg-gray-700/40')}>
                {modemName(id)}
              </button>
            ))}
          </div>
        )}
        <div className="flex-1 overflow-y-auto">
          {filtered.length === 0 ? (
            <div className="p-6 text-center text-gray-500 text-sm">{loading ? '…' : t('msg_empty')}</div>
          ) : filtered.map(c => {
            const a = avatarOf(c.number)
            const active = c.key === selected
            return (
              <button key={c.key} onClick={() => { setSelected(c.key); setComposing(false) }}
                className={clsx('w-full flex items-center gap-3 px-3 py-2.5 text-left transition-colors', active && !composing ? 'bg-blue-500/15' : 'hover:bg-gray-700/30')}>
                <div className={clsx('w-10 h-10 rounded-full flex items-center justify-center text-white text-xs font-bold shrink-0', a.color)}>{a.label}</div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-white truncate flex-1">{displayName(c.number)}</span>
                    <span className="text-[11px] text-gray-500 shrink-0">{c.last ? convTime(c.last.created_at) : (c.scheduled[0] && fmtSchedule(c.scheduled[0].send_once_at!))}</span>
                  </div>
                  <div className="flex items-center gap-1.5">
                    <span className={clsx('shrink-0 px-1.5 py-0.5 rounded text-[10px] border', cardTint(c.modemId))}>{modemName(c.modemId)}</span>
                    {c.last ? (
                      <span className="text-xs text-gray-400 truncate">
                        {c.last.direction === 'outbound' && <span className="text-gray-500">{t('msg_you')}: </span>}
                        {c.last.content}
                      </span>
                    ) : c.scheduled.length > 0 ? (
                      <span className="text-xs text-blue-300/80 truncate flex items-center gap-1">
                        <Clock className="w-3 h-3 shrink-0" />{c.scheduled[0].content}
                      </span>
                    ) : null}
                    {c.last && c.scheduled.length > 0 && (
                      <Clock className="w-3 h-3 text-blue-400 shrink-0" />
                    )}
                  </div>
                </div>
              </button>
            )
          })}
        </div>
      </aside>

      {/* ── 右：新建 / 会话线程 + 回复框 ── */}
      <section className={clsx('flex-1 min-w-0 min-h-0 flex-col', isMobile && !showThread ? 'hidden' : 'flex')}>
        {composing ? (
          <>
            <header className="px-4 py-3 flex items-center gap-3">
              <span className="text-sm font-semibold text-white flex-1">{t('msg_new')}</span>
              <button onClick={() => setComposing(false)} className="p-1 rounded text-gray-400 hover:text-white hover:bg-gray-700/50"><X className="w-4 h-4" /></button>
            </header>
            <div className="p-4 space-y-3 border-b border-gray-700/60">
              <label className="block">
                <span className="text-xs text-gray-400">{t('send_select_sim')}</span>
                <select value={composeCard} onChange={e => setComposeCard(e.target.value === '' ? '' : Number(e.target.value))}
                  className="mt-1 w-full bg-gray-800 border border-gray-600 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-blue-500">
                  <option value="">{t('send_select_sim')}</option>
                  {sendableModems.map(m => (
                    <option key={m.id} value={m.id}>{(m.alias || m.operator || `SIM ${m.id}`)}{m.phone_number ? ` (${m.phone_number})` : ''}</option>
                  ))}
                </select>
              </label>
              <label className="block">
                <span className="text-xs text-gray-400">{t('send_to')}</span>
                <input value={composeTo} onChange={e => setComposeTo(e.target.value)} placeholder="+8613800138000" autoFocus
                  className="mt-1 w-full bg-gray-800 border border-gray-600 rounded-lg px-3 py-2 text-sm text-white font-mono placeholder-gray-500 focus:outline-none focus:border-blue-500" />
              </label>
            </div>
            <div className="flex-1" />
            <div className="p-3">
              <div className={clsx('flex flex-col rounded-2xl transition-colors',
                scheduleCtx === 'compose' && 'border border-dashed border-blue-400/70 bg-blue-500/5')}>
                {scheduleCtx === 'compose' && (
                  <div className="flex items-center gap-2 px-3 pt-2 pb-1">
                    <Clock className="w-4 h-4 text-blue-400 shrink-0" />
                    <input type="datetime-local" min={nowLocal()} value={scheduleAt} onChange={e => setScheduleAt(e.target.value)}
                      className="flex-1 min-w-0 bg-transparent text-blue-300 text-sm font-medium focus:outline-none [color-scheme:dark]" />
                    <button onClick={() => { setScheduleCtx(null); setScheduleAt('') }} className="shrink-0 p-1 text-blue-300/70 hover:text-white"><X className="w-4 h-4" /></button>
                  </div>
                )}
                <div className="flex items-end gap-2 p-1">
                  <button onClick={() => toggleSchedule('compose')}
                    title={t('msg_schedule_send')}
                    className={clsx('shrink-0 w-9 h-9 flex items-center justify-center rounded-full transition-colors',
                      scheduleCtx === 'compose' ? 'text-blue-400' : 'text-gray-400 hover:text-blue-300 hover:bg-blue-500/10')}>
                    <Clock className="w-4 h-4" />
                  </button>
                  <textarea value={composeText} onChange={e => setComposeText(e.target.value)}
                    onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); scheduleCtx === 'compose' ? submitSchedule() : sendCompose() } }}
                    rows={1} placeholder={scheduleCtx === 'compose' ? t('msg_send_later') : t('msg_reply_ph')}
                    className={clsx('flex-1 resize-none rounded-xl px-3.5 py-2 text-sm text-white placeholder-gray-500 focus:outline-none max-h-32',
                      scheduleCtx === 'compose' ? 'bg-transparent' : 'bg-gray-800 border border-gray-600 focus:border-blue-500')} />
                  <button
                    onClick={() => scheduleCtx === 'compose' ? submitSchedule() : sendCompose()}
                    disabled={scheduleCtx === 'compose'
                      ? (!scheduleAt || !composeTo.trim() || !composeText.trim() || composeCard === '' || scheduling)
                      : (!composeTo.trim() || !composeText.trim() || composeCard === '')}
                    className="shrink-0 w-9 h-9 flex items-center justify-center rounded-full bg-blue-500 hover:bg-blue-400 text-white shadow-lg shadow-blue-500/50 disabled:opacity-70 transition-all">
                    <Send className="w-4 h-4" />
                  </button>
                </div>
              </div>
            </div>
          </>
        ) : current ? (
          <>
            <header className="relative px-4 py-3 flex flex-col items-center gap-1">
              <button onClick={() => setSelected(null)}
                className="md:hidden absolute left-3 top-1/2 -translate-y-1/2 p-1.5 rounded-lg text-gray-300 hover:text-white hover:bg-gray-700/50">
                <ChevronLeft className="w-5 h-5" />
              </button>
              <div className={clsx('w-9 h-9 rounded-full flex items-center justify-center text-white text-xs font-bold', avatarOf(current.number).color)}>{avatarOf(current.number).label}</div>
              <div className="text-sm font-semibold text-white truncate max-w-[80%] text-center">{displayName(current.number)}</div>
              {displayName(current.number) !== current.number && (
                <div className="text-[11px] text-gray-500 font-mono">{current.number}</div>
              )}
              <span className={clsx('px-1.5 py-0.5 rounded text-[10px] border', cardTint(current.modemId))}>{modemName(current.modemId)}</span>
            </header>

            <div ref={threadRef} className="flex-1 overflow-y-auto px-4 py-4 space-y-2">
              {current.msgs.map((m, i) => {
                const out = m.direction === 'outbound'
                const showTime = i === 0 || (+new Date(m.created_at) - +new Date(current.msgs[i - 1].created_at)) > 5 * 60 * 1000
                return (
                  <div key={m.id}>
                    {showTime && (
                      <div className="text-center text-[11px] text-gray-500 my-3">
                        <span className="font-semibold text-gray-400">{daySep(m.created_at).date}</span> {daySep(m.created_at).time}
                      </div>
                    )}
                    <div className={clsx('flex', out ? 'justify-end' : 'justify-start')}>
                      <div className={clsx('max-w-[70%] rounded-2xl px-3.5 py-2 text-sm break-words',
                        out
                          ? m.status === 'failed' ? 'bg-red-500/20 text-red-200 border border-red-500/40' : 'bg-blue-600 text-white'
                          : 'bg-gray-700 text-gray-100')}>
                        <div className="whitespace-pre-wrap">{m.content}</div>
                        <div className={clsx('flex items-center gap-1.5 mt-1 text-[10px]', out ? 'text-blue-200/70 justify-end' : 'text-gray-400')}>
                          {m.status === 'pending'
                            ? <Loader2 className="w-3 h-3 animate-spin" />
                            : (m.channel === 'vowifi' ? <Wifi className="w-3 h-3" /> : <Radio className="w-3 h-3" />)}
                          <span>{m.status === 'pending' ? t('msg_sending') : format(new Date(m.created_at), 'HH:mm')}</span>
                          {out && m.status === 'failed' && <span className="text-red-300">· {t('hist_status_failed')}</span>}
                        </div>
                      </div>
                    </div>
                  </div>
                )
              })}

              {/* 待发的定时消息（右对齐、虚线、时钟标记，可取消）*/}
              {current.scheduled.map(tk => (
                <div key={`sch-${tk.id}`} className="flex justify-end">
                  <div className="max-w-[70%] rounded-2xl px-3.5 py-2 text-sm break-words bg-blue-500/10 text-blue-100 border border-dashed border-blue-400/50">
                    <div className="whitespace-pre-wrap">{tk.content}</div>
                    <div className="flex items-center gap-1.5 mt-1 text-[10px] text-blue-200/80 justify-end">
                      <Clock className="w-3 h-3" />
                      <span>{t('msg_scheduled_at')} {fmtSchedule(tk.send_once_at!)}</span>
                      <button onClick={() => cancelScheduled(tk.id)} className="ml-1 hover:text-red-300" title={t('cancel')}>
                        <X className="w-3 h-3" />
                      </button>
                    </div>
                  </div>
                </div>
              ))}
            </div>

            {/* 回复框（Apple「稍后发送」风格：定时时整框变蓝色虚线，顶部内嵌可分段编辑的日期）*/}
            <div className="p-3">
              <div className={clsx('flex flex-col rounded-2xl transition-colors',
                scheduleCtx === 'reply' && 'border border-dashed border-blue-400/70 bg-blue-500/5')}>
                {scheduleCtx === 'reply' && (
                  <div className="flex items-center gap-2 px-3 pt-2 pb-1">
                    <Clock className="w-4 h-4 text-blue-400 shrink-0" />
                    <input type="datetime-local" min={nowLocal()} value={scheduleAt} onChange={e => setScheduleAt(e.target.value)}
                      className="flex-1 min-w-0 bg-transparent text-blue-300 text-sm font-medium focus:outline-none [color-scheme:dark]" />
                    <button onClick={() => { setScheduleCtx(null); setScheduleAt('') }} className="shrink-0 p-1 text-blue-300/70 hover:text-white"><X className="w-4 h-4" /></button>
                  </div>
                )}
                <div className="flex items-end gap-2 p-1">
                  <button onClick={() => toggleSchedule('reply')}
                    title={t('msg_schedule_send')}
                    className={clsx('shrink-0 w-9 h-9 flex items-center justify-center rounded-full transition-colors',
                      scheduleCtx === 'reply' ? 'text-blue-400' : 'text-gray-400 hover:text-blue-300 hover:bg-blue-500/10')}>
                    <Clock className="w-4 h-4" />
                  </button>
                  <textarea
                    value={input}
                    onChange={e => setInput(e.target.value)}
                    onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); scheduleCtx === 'reply' ? submitSchedule() : reply() } }}
                    rows={1}
                    placeholder={scheduleCtx === 'reply' ? t('msg_send_later') : t('msg_reply_ph')}
                    className={clsx('flex-1 resize-none rounded-xl px-3.5 py-2 text-sm text-white placeholder-gray-500 focus:outline-none max-h-32',
                      scheduleCtx === 'reply' ? 'bg-transparent' : 'bg-gray-800 border border-gray-600 focus:border-blue-500')}
                  />
                  <button
                    onClick={() => scheduleCtx === 'reply' ? submitSchedule() : reply()}
                    disabled={scheduleCtx === 'reply' ? (!scheduleAt || !input.trim() || scheduling) : !input.trim()}
                    className="shrink-0 w-9 h-9 flex items-center justify-center rounded-full bg-blue-500 hover:bg-blue-400 text-white shadow-lg shadow-blue-500/50 disabled:opacity-70 transition-all">
                    <Send className="w-4 h-4" />
                  </button>
                </div>
              </div>
            </div>
          </>
        ) : (
          <div className="flex-1 flex flex-col items-center justify-center text-gray-500 gap-3">
            <MessageSquare className="w-10 h-10 opacity-30" />
            <span className="text-sm">{loading ? '…' : t('msg_empty')}</span>
          </div>
        )}
      </section>
    </div>
  )
}
