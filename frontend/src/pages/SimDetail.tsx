import { useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import {
  ArrowLeft, RefreshCw, Wifi, WifiOff, AlertCircle, HelpCircle, Signal,
  Clock, MessageSquare, Upload, Download, Pencil, Check, X,
} from 'lucide-react'
import clsx from 'clsx'
import { getModemDetailApi, updateModemApi, refreshModemApi, setVowifiModeApi, setAirplaneApi, type ModemDetail } from '../api/modems'
import { useT } from '../i18n'
import { useLangStore } from '../store/langStore'
import { useAuthStore } from '../store/authStore'

function fmtBytes(bytes: number | null | undefined): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = bytes, u = 0
  while (v >= 1024 && u < units.length - 1) { v /= 1024; u++ }
  return `${v.toFixed(u === 0 ? 0 : 1)} ${units[u]}`
}

function fmtDuration(seconds: number | null | undefined): string {
  if (!seconds) return '—'
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = seconds % 60
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

function techLabel(techs: string | null | undefined): string {
  if (!techs) return '—'
  const map: Record<string, string> = {
    lte: '4G LTE', umts: '3G UMTS', gsm: '2G GSM',
    hspa: '3G HSPA', 'hspa+': '3G HSPA+', nr: '5G NR',
  }
  return techs.split(',').map(t => map[t.trim().toLowerCase()] ?? t.trim().toUpperCase()).join(' / ')
}

// 在线状态徽标：蜂窝在线 或 VoWiFi 在线都算“在线”，用不同图标区分（📶VoWiFi / 信号格蜂窝）。
const StatusBadge = ({ status, vowifiOnline, t }: { status: string; vowifiOnline?: boolean; t: ReturnType<typeof useT> }) => {
  if (vowifiOnline) {
    return (
      <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium border bg-green-500/20 text-green-400 border-green-500/30">
        <Wifi className="w-3 h-3" /> {t('status_connected')} · VoWiFi
      </span>
    )
  }
  const cfg = {
    connected:    { icon: Signal,      cls: 'bg-green-500/20 text-green-400 border-green-500/30',    label: `${t('status_connected')} · ${t('status_cellular')}` },
    disconnected: { icon: WifiOff,     cls: 'bg-gray-500/20 text-gray-400 border-gray-500/30',       label: t('status_disconnected') },
    error:        { icon: AlertCircle, cls: 'bg-red-500/20 text-red-400 border-red-500/30',          label: t('status_error') },
    unknown:      { icon: HelpCircle,  cls: 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30', label: t('status_unknown') },
  }[status] ?? { icon: HelpCircle, cls: 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30', label: status }
  const Icon = cfg.icon
  return (
    <span className={clsx('inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium border', cfg.cls)}>
      <Icon className="w-3 h-3" /> {cfg.label}
    </span>
  )
}

// 紧凑的“标签在上、值在下”字段（用于 VoWiFi 卡片的运行信息网格，便于以后扩展）
const Field = ({ label, value, title }: { label: string; value: string; title?: string }) => (
  <div className="min-w-0">
    <div className="text-[10px] text-gray-500">{label}</div>
    <div className="text-[11px] text-gray-200 font-mono truncate" title={title ?? value}>{value}</div>
  </div>
)

const SignalBars = ({ quality }: { quality: number }) => {
  const bars = Math.round((quality / 100) * 5)
  const color = bars >= 4 ? 'bg-green-400' : bars >= 2 ? 'bg-yellow-400' : 'bg-red-400'
  return (
    <div className="flex items-end gap-1 h-5">
      {[1, 2, 3, 4, 5].map(i => (
        <div key={i} className={clsx('w-2 rounded-sm', i <= bars ? color : 'bg-gray-600')}
          style={{ height: `${i * 20}%` }} />
      ))}
      <span className="ml-1 text-sm text-gray-300">{quality}%</span>
    </div>
  )
}

interface InfoRowProps { label: string; value: React.ReactNode; stale?: boolean; staleTag?: string }
const InfoRow = ({ label, value, stale, staleTag }: InfoRowProps) => (
  <div className={clsx('flex items-center justify-between py-2.5 border-b border-gray-700/50 last:border-0', stale && 'opacity-50')}>
    <span className="text-sm text-gray-400 flex items-center gap-1.5">
      {label}
      {stale && <span className="text-[10px] px-1 rounded bg-amber-500/15 text-amber-500/90 border border-amber-500/25" title={staleTag}>旧</span>}
    </span>
    <span className="text-sm text-gray-100 font-medium text-right max-w-[60%]">{value}</span>
  </div>
)

// Panel 统一的详情卡片容器：圆角、细边框、标题，可标记为“旧值”。
const Panel = ({ title, stale, staleTag, children }: { title: string; stale?: boolean; staleTag?: string; children: React.ReactNode }) => (
  <section className={clsx('rounded-2xl border border-gray-700/60 bg-gray-800/40 p-5', stale && 'opacity-60')}>
    <h2 className="text-xs font-semibold text-gray-400 uppercase tracking-wider mb-2 flex items-center gap-1.5">
      {title}
      {stale && <span className="text-[10px] px-1 rounded bg-amber-500/15 text-amber-500/90 border border-amber-500/25 normal-case tracking-normal" title={staleTag}>旧</span>}
    </h2>
    {children}
  </section>
)


export default function SimDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const t = useT()
  const lang = useLangStore(s => s.lang)
  const [modem, setModem] = useState<ModemDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [editingAlias, setEditingAlias] = useState(false)
  const [alias, setAlias] = useState('')
  const isAdmin = useAuthStore(s => s.user?.role === 'admin')
  const [vwEpdg, setVwEpdg] = useState('')
  const [vwAtPort, setVwAtPort] = useState('')
  const [vwSaving, setVwSaving] = useState(false)

  const fmtTime = (iso: string | null | undefined) => {
    if (!iso) return t('none')
    return new Date(iso).toLocaleString(lang === 'zh' ? 'zh-CN' : 'en-US')
  }

  const regLabel = (state: string | null | undefined): string => {
    if (!state) return t('none')
    const map: Record<string, string> = {
      home: t('detail_reg_home'), roaming: t('detail_reg_roaming'),
      searching: t('detail_reg_searching'), denied: t('detail_reg_denied'), idle: t('detail_reg_idle'),
    }
    return map[state.toLowerCase()] ?? state
  }

  const load = () => {
    if (!id) return
    setLoading(true)
    getModemDetailApi(Number(id))
      .then(r => {
        setModem(r.data); setAlias(r.data.alias ?? '')
        setVwEpdg(r.data.vowifi_epdg_ip ?? ''); setVwAtPort(r.data.vowifi_at_port ?? '')
      })
      .finally(() => setLoading(false))
  }

  // 切换 VoWiFi 模式 / 保存 ePDG、串口配置（仅管理员）
  const saveVowifi = async (mode?: boolean) => {
    if (!id) return
    setVwSaving(true)
    try {
      await setVowifiModeApi(Number(id), {
        ...(mode !== undefined ? { vowifi_mode: mode } : {}),
        vowifi_epdg_ip: vwEpdg,
        vowifi_at_port: vwAtPort,
      })
      load()
    } finally { setVwSaving(false) }
  }

  // 切换飞行模式（VoWiFi 期间关射频，不在蜂窝基站注册）
  const [airSaving, setAirSaving] = useState(false)
  const saveAirplane = async (on: boolean) => {
    if (!id) return
    setAirSaving(true)
    try { await setAirplaneApi(Number(id), on); load() }
    catch (e: any) { alert(e?.response?.data?.msg || '切换飞行模式失败') }
    finally { setAirSaving(false) }
  }

  useEffect(() => { load() }, [id])

  // VoWiFi 模式开启但会话尚未就绪时，轮询刷新以实时显示各步骤状态
  useEffect(() => {
    if (!modem?.vowifi_mode || modem?.vowifi_running) return
    const timer = setInterval(() => {
      if (!id) return
      getModemDetailApi(Number(id)).then(r => setModem(r.data)).catch(() => {})
    }, 3000)
    return () => clearInterval(timer)
  }, [id, modem?.vowifi_mode, modem?.vowifi_running])

  const handleRefresh = async () => {
    if (!id) return
    setRefreshing(true)
    try { await refreshModemApi(Number(id)); await load() }
    finally { setRefreshing(false) }
  }

  const saveAlias = async () => {
    if (!id) return
    await updateModemApi(Number(id), { alias })
    setEditingAlias(false)
    load()
  }

  if (loading) return (
    <div className="p-6 flex items-center gap-2 text-gray-400">
      <RefreshCw className="w-4 h-4 animate-spin" /> {t('loading')}
    </div>
  )

  if (!modem) return <div className="p-6 text-red-400">{t('detail_not_found')}</div>

  const displayName = modem.alias || `SIM ${modem.id}`
  // 飞行模式：射频已关，蜂窝侧的运营商/制式/信号/注册都无效，清成 —（不再当“旧值”展示）
  const rfOff = modem.vowifi_mode && modem.vowifi_airplane

  return (
    <div className="p-6 space-y-5 max-w-5xl mx-auto">
      {/* ── 顶部状态栏（类手机状态栏：名称 · 信号/制式/运营商 · 刷新）── */}
      <div className="flex items-center gap-3 flex-wrap">
        <button onClick={() => navigate(-1)}
          className="p-2 rounded-lg text-gray-400 hover:text-white hover:bg-gray-800 transition-colors shrink-0">
          <ArrowLeft className="w-5 h-5" />
        </button>
        {editingAlias ? (
          <div className="flex items-center gap-2 flex-1">
            <input
              className="bg-gray-800 border border-gray-600 rounded-lg px-3 py-1.5 text-white text-lg font-bold focus:outline-none focus:border-blue-500 w-48"
              value={alias}
              onChange={e => setAlias(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && saveAlias()}
              placeholder={t('detail_alias_ph')}
              autoFocus
            />
            <button onClick={saveAlias} className="p-1.5 rounded text-green-400 hover:bg-gray-800"><Check className="w-4 h-4" /></button>
            <button onClick={() => setEditingAlias(false)} className="p-1.5 rounded text-gray-400 hover:bg-gray-800"><X className="w-4 h-4" /></button>
          </div>
        ) : (
          <div className="flex items-center gap-2 shrink-0">
            <h1 className="text-xl font-bold text-white truncate">{displayName}</h1>
            <button onClick={() => setEditingAlias(true)}
              className="p-1 rounded text-gray-500 hover:text-gray-300 hover:bg-gray-800 transition-colors">
              <Pencil className="w-3.5 h-3.5" />
            </button>
            <StatusBadge status={modem.status} vowifiOnline={modem.vowifi_mode && modem.vowifi_running} t={t} />
          </div>
        )}

        {/* 状态栏指标：信号格 + % + 制式 + 运营商（VoWiFi 下为旧值置灰）*/}
        <div className={clsx('flex items-center gap-2.5 text-sm', modem.vowifi_mode && 'opacity-60')}>
          {modem.vowifi_mode && modem.vowifi_airplane ? (
            // 飞行模式：射频已关，没有实时信号
            <span className="text-gray-400">{t('detail_signal')} —</span>
          ) : (
            <div className="flex items-center gap-1.5">
              <div className="flex items-end gap-0.5 h-4">
                {[1, 2, 3, 4, 5].map(i => {
                  const bars = Math.round((modem.signal_quality / 100) * 5)
                  const color = bars >= 4 ? 'bg-green-400' : bars >= 2 ? 'bg-yellow-400' : 'bg-red-400'
                  return <div key={i} className={clsx('w-1 rounded-sm', i <= bars ? color : 'bg-gray-600')} style={{ height: `${i * 20}%` }} />
                })}
              </div>
              <span className="font-semibold text-white">{modem.signal_quality}%</span>
            </div>
          )}
          {!rfOff && (
            <>
              <span className="text-gray-600">·</span>
              <span className="text-gray-300">{techLabel(modem.access_technologies)}</span>
              <span className="text-gray-400 truncate max-w-[160px] hidden md:inline">{modem.operator || t('none')}</span>
            </>
          )}
        </div>

        <button
          onClick={handleRefresh}
          disabled={refreshing}
          className="flex items-center gap-1.5 px-3 py-1.5 ml-auto bg-gray-800 hover:bg-gray-700 text-gray-300 rounded-lg text-sm transition-colors disabled:opacity-50 shrink-0"
        >
          <RefreshCw className={clsx('w-4 h-4', refreshing && 'animate-spin')} />
          <span className="hidden sm:inline">{refreshing ? t('detail_refreshing') : t('detail_refresh')}</span>
        </button>
      </div>

      {/* ── 副状态条：号码 · 注册（VoWiFi 旧值提示）── */}
      <div className="flex items-center flex-wrap gap-x-4 gap-y-1.5 pl-11 -mt-2 text-xs text-gray-400">
        <span className="font-mono text-gray-200">{modem.phone_number || t('unknown')}</span>
        <span className={clsx(modem.vowifi_mode && modem.vowifi_airplane && 'text-amber-400')}>{modem.vowifi_mode && modem.vowifi_airplane ? t('detail_reg_airplane') : regLabel(modem.registration_state)}</span>
        {modem.vowifi_mode && <span className="text-amber-500/80">⚠ {t('vowifi_stale_note')}</span>}
      </div>

      {/* ── 关键指标卡片：时长 / 上行 / 下行 / 今日发送 ── */}
      <div className="rounded-2xl border border-gray-700/60 bg-gray-800/40 p-4 grid grid-cols-2 sm:grid-cols-4 gap-4">
        {[
          { icon: <Clock className="w-4 h-4 text-blue-400 shrink-0" />, label: t('detail_duration'), value: fmtDuration(modem.connection_duration), dim: modem.vowifi_mode },
          { icon: <Upload className="w-4 h-4 text-orange-400 shrink-0" />, label: t('detail_upload'), value: fmtBytes(modem.tx_bytes), dim: modem.vowifi_mode },
          { icon: <Download className="w-4 h-4 text-purple-400 shrink-0" />, label: t('detail_download'), value: fmtBytes(modem.rx_bytes), dim: modem.vowifi_mode },
          { icon: <MessageSquare className="w-4 h-4 text-green-400 shrink-0" />, label: t('detail_sms_today'), value: String(modem.sms_today), dim: false },
        ].map((m, i) => (
          <div key={i} className={clsx('flex items-center gap-2', m.dim && 'opacity-60')}>
            {m.icon}
            <div className="min-w-0">
              <div className="text-[10px] text-gray-500">{m.label}</div>
              <div className="text-sm font-semibold text-gray-100 truncate">{m.value}</div>
            </div>
          </div>
        ))}
      </div>


      {/* ── 详情两栏 ── */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-5">
      {/* VoWiFi 模式：紧凑卡片 */}
      {isAdmin && (() => {
        // 直接按后端真实的建链步骤逐段渲染（实事求是，不套用固定的 SIM/Access/... 分组）。
        // 每个步骤给一个简短徽标；顺序与后端 StepOrder 一致。
        const shortLabel: Record<string, string> = {
          '独占该卡': '独占卡',
          'IKE_SA_INIT': 'IKE_SA',
          'EAP-AKA 认证': 'EAP-AKA',
          'IKE_AUTH 地址分配': 'IKE_AUTH',
          'IMS 注册': '注册',
          'SUBSCRIBE 订阅': '订阅',
          '接收循环': '接收',
        }
        // 固定的 7 个建链阶段（顺序同后端 StepOrder）。用它兜底：后端还没返回步骤时
        // 也显示全灰占位，不会出现“建立中却啥都没有”。
        const stageOrder = ['独占该卡', 'IKE_SA_INIT', 'EAP-AKA 认证', 'IKE_AUTH 地址分配', 'IMS 注册', 'SUBSCRIBE 订阅', '接收循环']
        const stepState = (n: string) => modem.vowifi_steps?.find(s => s.name === n)?.state ?? 'pending'
        const groups = stageOrder.map(n => ({ key: shortLabel[n] ?? n, state: stepState(n), name: n }))
        const barColor: Record<string, string> = {
          ok: 'bg-green-400', running: 'bg-yellow-400 animate-pulse', fail: 'bg-red-400', pending: 'bg-gray-600',
        }
        // 步骤胶囊底色（浅色/深色下都保留辨识度）
        const pillCls: Record<string, string> = {
          ok: 'bg-green-500/15 text-green-500', running: 'bg-yellow-500/15 text-yellow-600',
          fail: 'bg-red-500/15 text-red-500', pending: 'bg-gray-500/15 text-gray-500',
        }
        const allOk = groups.every(g => g.state === 'ok')
        const anyFail = groups.some(g => g.state === 'fail')
        const status = !modem.vowifi_mode
          ? { dot: 'bg-gray-400', text: 'text-gray-400', label: t('vowifi_st_off') }
          : allOk
            ? { dot: 'bg-green-400', text: 'text-green-400', label: t('vowifi_all_ready') }
            : anyFail
              ? { dot: 'bg-red-400', text: 'text-red-300', label: t('vowifi_setup_failed') }
              : { dot: 'bg-yellow-400 animate-pulse', text: 'text-yellow-300', label: t('vowifi_setting_up') }
        const failStep = modem.vowifi_steps?.find(s => s.state === 'fail')
        const info = modem.vowifi_info
        return (
          <section className="rounded-2xl border border-gray-700/60 bg-gray-800/40 p-5 space-y-2.5">
            {/* 头部：状态点 + 标题 + 状态文本 +（就绪时）阶段小圆点 + 主开关 */}
            <div className="flex items-center gap-2">
              <span className={clsx('w-2 h-2 rounded-full shrink-0', status.dot)} />
              <span className={clsx('text-sm font-semibold shrink-0', status.text)}>WiFi-Calling</span>
              <span className={clsx('text-[11px] shrink-0', status.text)}>· {status.label}</span>
              {/* 飞行模式：独立常显（与 WiFi-Calling 解耦，关了 VoWiFi 也能让卡不在蜂窝注册）*/}
              <div className="flex items-center gap-1.5 ml-auto shrink-0" title="飞行模式（关射频，不在蜂窝基站注册）">
                <span className="text-[11px] text-gray-400">✈️ 飞行</span>
                <button
                  onClick={() => saveAirplane(!modem.vowifi_airplane)}
                  disabled={airSaving}
                  className={clsx('relative inline-flex h-5 w-9 items-center rounded-full transition-colors disabled:opacity-50',
                    modem.vowifi_airplane ? 'bg-amber-500' : 'bg-gray-600')}
                >
                  <span className={clsx('inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform',
                    modem.vowifi_airplane ? 'translate-x-4' : 'translate-x-1')} />
                </button>
              </div>
              <button
                onClick={() => saveVowifi(!modem.vowifi_mode)}
                disabled={vwSaving}
                className={clsx('relative inline-flex h-5 w-9 items-center rounded-full transition-colors disabled:opacity-50 shrink-0',
                  modem.vowifi_mode ? 'bg-blue-600' : 'bg-gray-600')}
                title={t('vowifi_toggle')}
              >
                <span className={clsx('inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform',
                  modem.vowifi_mode ? 'translate-x-4' : 'translate-x-1')} />
              </button>
            </div>

            {modem.vowifi_mode && (
              <>
                {/* 建链步骤：小胶囊，带步骤名称与状态色（放在标题下方，一行换行排布）*/}
                <div className="flex flex-wrap gap-1.5">
                  {groups.map(g => (
                    <span key={g.name} title={`${g.name}: ${g.state}`}
                      className={clsx('inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-medium', pillCls[g.state])}>
                      <span className={clsx('w-1.5 h-1.5 rounded-full shrink-0', barColor[g.state])} />
                      {g.key}
                    </span>
                  ))}
                </div>
                {failStep && (
                  <p className="text-[11px] text-red-400 truncate" title={failStep.detail}>✕ {failStep.name}{failStep.detail ? '：' + failStep.detail : ''}</p>
                )}

                {/* 运行信息：列排布（每项一行，标签左、值右）*/}
                {info && (
                  <div>
                    <p className="text-[10px] uppercase tracking-wider text-gray-500 mb-1">{t('vowifi_runtime')}</p>
                    <div className="divide-y divide-gray-700/40">
                      {[
                        { label: 'ePDG', value: info.epdg_ip || '—' },
                        { label: t('vowifi_tunnel_ip'), value: info.tunnel_ipv6 || '—' },
                        { label: 'P-CSCF', value: String(info.pcscf_count) },
                        { label: t('detail_phone'), value: modem.phone_number || '—' },
                      ].map((r, i) => (
                        <div key={i} className="flex items-center justify-between gap-4 py-1 text-[11px]">
                          <span className="text-gray-500 shrink-0">{r.label}</span>
                          <span className="text-gray-200 font-mono truncate" title={r.value}>{r.value}</span>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {/* 设置：列排布（标签左、控件右）*/}
                <div className="pt-2.5 border-t border-gray-700/60 space-y-2">
                  <p className="text-[10px] uppercase tracking-wider text-gray-500">{t('vowifi_settings')}</p>

                  {/* ePDG 地址：标签左、输入右 */}
                  <div className="flex items-center gap-3 text-[11px]">
                    <span className="text-gray-500 w-14 shrink-0">{t('vowifi_epdg')}</span>
                    <input className="flex-1 min-w-0 bg-gray-900 border border-gray-600 rounded px-2 py-1 text-[11px] text-white font-mono focus:outline-none focus:border-blue-500"
                      value={vwEpdg} onChange={e => setVwEpdg(e.target.value)} placeholder="87.194.89.8" />
                  </div>

                  {/* AT 串口：标签左、输入右 */}
                  <div className="flex items-center gap-3 text-[11px]">
                    <span className="text-gray-500 w-14 shrink-0">{t('vowifi_atport')}</span>
                    <input className="flex-1 min-w-0 bg-gray-900 border border-gray-600 rounded px-2 py-1 text-[11px] text-white font-mono focus:outline-none focus:border-blue-500"
                      value={vwAtPort} onChange={e => setVwAtPort(e.target.value)} placeholder="/dev/ttyUSB2" />
                  </div>

                  <button onClick={() => saveVowifi()} disabled={vwSaving}
                    className="w-full px-2.5 py-1 bg-gray-700 hover:bg-gray-600 text-gray-200 rounded text-[11px] transition-colors disabled:opacity-50">
                    {t('vowifi_save')}
                  </button>
                </div>
              </>
            )}
          </section>
        )
      })()}
        {/* SIM 卡身份 */}
        <Panel title={t('detail_sim_info')}>
          <InfoRow label={t('detail_phone')} value={modem.phone_number || t('unknown')} />
          <InfoRow label="IMSI" value={<span className="font-mono text-xs">{(modem as any).imsi || t('none')}</span>} />
          <InfoRow label="ICCID" value={<span className="font-mono text-xs">{(modem as any).iccid || t('none')}</span>} />
          <InfoRow label={t('detail_imei')} value={<span className="font-mono text-xs">{modem.imei || t('none')}</span>} />
          <InfoRow label={t('detail_sim_operator')} value={
            (modem as any).sim_operator_name
              ? `${(modem as any).sim_operator_name}${(modem as any).sim_operator_code ? ` (${(modem as any).sim_operator_code})` : ''}`
              : t('none')
          } />
          <InfoRow label={t('detail_operator')} value={rfOff ? t('none') : (modem.operator || t('none'))} stale={modem.vowifi_mode && !rfOff} staleTag={t('vowifi_stale_note')} />
          <InfoRow label={t('detail_reg')} value={rfOff ? t('detail_reg_airplane') : regLabel(modem.registration_state)} stale={modem.vowifi_mode && !rfOff} staleTag={t('vowifi_stale_note')} />
          <InfoRow label={t('detail_tech')} value={rfOff ? t('none') : techLabel(modem.access_technologies)} stale={modem.vowifi_mode && !rfOff} staleTag={t('vowifi_stale_note')} />
        </Panel>

        {/* 硬件 */}
        <Panel title={t('detail_hardware')}>
          <InfoRow label={t('detail_manufacturer')} value={modem.manufacturer || t('none')} />
          <InfoRow label={t('detail_model')} value={modem.model || t('none')} />
          <InfoRow label={t('detail_firmware')} value={<span className="font-mono text-xs">{(modem as any).firmware_revision || t('none')}</span>} />
          <InfoRow label={t('detail_hw_rev')} value={(modem as any).hardware_revision || t('none')} />
          <InfoRow label={t('detail_plugin')} value={(modem as any).plugin || t('none')} />
          <InfoRow label={t('detail_device')} value={<span className="font-mono text-xs">{modem.device_path || t('none')}</span>} />
          <InfoRow label={t('detail_conn_since')} value={fmtTime(modem.created_at)} />
          <InfoRow label={t('detail_last_seen')} value={fmtTime(modem.last_seen)} stale={modem.vowifi_mode} staleTag={t('vowifi_stale_note')} />
        </Panel>

        {/* 网络：模式 + 频段 */}
        <Panel title={t('detail_network_mode')} stale={modem.vowifi_mode} staleTag={t('vowifi_stale_note')}>
          <InfoRow label={t('detail_current_mode')} value={(modem as any).current_modes || t('none')} />
          <InfoRow label={t('detail_ports')} value={<span className="font-mono text-xs">{(modem as any).ports || t('none')}</span>} />
          <div className="pt-3">
            <div className="text-xs text-gray-500 mb-1.5">{t('detail_bands')}</div>
            <div className="flex flex-wrap gap-1.5">
              {((modem as any).current_bands || '').split(',').filter(Boolean).map((b: string) => (
                <span key={b} className="px-2 py-0.5 bg-blue-500/10 border border-blue-500/30 text-blue-300 text-xs rounded-full font-mono">{b.trim()}</span>
              ))}
              {!(modem as any).current_bands && <span className="text-sm text-gray-500">{t('none')}</span>}
            </div>
          </div>
        </Panel>

        {/* 短信统计 + 流量 */}
        <Panel title={t('detail_sms_stats')}>
          <div className="grid grid-cols-3 gap-3 pb-3 mb-1 border-b border-gray-700/50">
            {[
              { label: t('detail_sms_sent'), value: modem.sms_sent, color: 'text-blue-400' },
              { label: t('detail_sms_recv'), value: modem.sms_received, color: 'text-green-400' },
              { label: t('detail_sms_today'), value: modem.sms_today, color: 'text-orange-400' },
            ].map(s => (
              <div key={s.label} className="text-center">
                <p className={clsx('text-2xl font-bold', s.color)}>{s.value}</p>
                <p className="text-[11px] text-gray-400 mt-0.5">{s.label}</p>
              </div>
            ))}
          </div>
          <div className={clsx(modem.vowifi_mode && 'opacity-50')}>
            <InfoRow label={t('detail_tx')} value={fmtBytes(modem.tx_bytes)} />
            <InfoRow label={t('detail_rx')} value={fmtBytes(modem.rx_bytes)} />
            <InfoRow label="Total" value={fmtBytes((modem.tx_bytes ?? 0) + (modem.rx_bytes ?? 0))} />
          </div>
        </Panel>
      </div>
    </div>
  )
}
