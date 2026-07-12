import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  RefreshCw, Wifi, WifiOff, Signal, AlertCircle, HelpCircle,
  ChevronRight, Upload, Download, ClipboardList, Clock, CheckCircle,
} from 'lucide-react'
import clsx from 'clsx'
import { getModemDetailApi, getModemsApi, type Modem, type ModemDetail } from '../api/modems'
import { useModemStore } from '../store/modemStore'
import { useAuthStore } from '../store/authStore'
import { useT } from '../i18n'
import { mySimRequestsApi, createSimRequestApi, type SimAccessRequest } from '../api/simRequests'

function fmtBytes(bytes: number | null | undefined): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = bytes, u = 0
  while (v >= 1024 && u < units.length - 1) { v /= 1024; u++ }
  return `${v.toFixed(u === 0 ? 0 : 1)} ${units[u]}`
}

function fmtDuration(seconds: number | null | undefined, t: ReturnType<typeof useT>): string {
  if (!seconds) return t('none')
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m ${seconds % 60}s`
}

function techLabel(techs: string | null | undefined): string {
  if (!techs) return '—'
  const map: Record<string, string> = {
    lte: '4G', umts: '3G', gsm: '2G', 'hspa+': 'H+', hspa: 'H', nr: '5G',
  }
  return techs.split(',').map(t => map[t.trim().toLowerCase()] ?? t.trim().toUpperCase()).join('/')
}

type Row = ModemDetail

export default function SimCards() {
  const navigate = useNavigate()
  const { modems } = useModemStore()
  const user = useAuthStore(s => s.user)
  const perm = useAuthStore(s => s.perm)()
  const [rows, setRows] = useState<Row[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [myRequests, setMyRequests] = useState<SimAccessRequest[]>([])
  const [applying, setApplying] = useState<number | null>(null)
  const t = useT()

  const isAdmin = user?.role === 'admin'

  const modemAccessStatus = (modemId: number): 'access' | 'pending' | 'approved' | 'none' => {
    if (isAdmin) return 'access'
    const now = new Date()
    const req = myRequests.find(r => r.modem_id === modemId)
    if (!req) return 'none'
    if (req.status === 'pending') return 'pending'
    if (req.status === 'approved' && req.granted_level === 'use' && (!req.expires_at || new Date(req.expires_at) > now)) return 'approved'
    return 'none'
  }

  const applyForModem = async (e: React.MouseEvent, modemId: number) => {
    e.stopPropagation()
    setApplying(modemId)
    try {
      await createSimRequestApi(modemId)
      const res = await mySimRequestsApi()
      setMyRequests(res.data)
    } catch (err: any) {
      alert(err.response?.data?.detail || t('sim_status_applying'))
    } finally {
      setApplying(null)
    }
  }

  const regLabel = (state: string | null | undefined): string => {
    if (!state) return t('none')
    const map: Record<string, string> = {
      home: t('reg_home'), roaming: t('reg_roaming'),
      searching: t('reg_searching'), denied: t('reg_denied'), idle: t('reg_idle'),
    }
    return map[state.toLowerCase()] ?? state
  }

  const StatusBadge = ({ status, vowifiOnline, airplane }: { status: string; vowifiOnline?: boolean; airplane?: boolean }) => {
    // VoWiFi 在线也算在线，用 Wifi 图标 + “· VoWiFi” 区分蜂窝在线
    if (vowifiOnline) {
      return (
        <span className="inline-flex items-center gap-1 text-xs font-medium text-green-400">
          <Wifi className="w-3.5 h-3.5" /> {t('status_connected')} · VoWiFi
        </span>
      )
    }
    // 飞行模式且 VoWiFi 未在线：射频关，不在任何网络注册
    if (airplane) {
      return (
        <span className="inline-flex items-center gap-1 text-xs font-medium text-amber-500">
          ✈️ {t('detail_reg_airplane')}
        </span>
      )
    }
    const cfg = {
      connected:    { icon: Signal,      cls: 'text-green-400',  label: `${t('status_connected')} · ${t('status_cellular')}` },
      disconnected: { icon: WifiOff,     cls: 'text-gray-400',   label: t('status_disconnected') },
      error:        { icon: AlertCircle, cls: 'text-red-400',    label: t('status_error') },
      unknown:      { icon: HelpCircle,  cls: 'text-yellow-400', label: t('status_unknown') },
    }[status] ?? { icon: HelpCircle, cls: 'text-yellow-400', label: status }
    const Icon = cfg.icon
    return (
      <span className={clsx('inline-flex items-center gap-1 text-xs font-medium', cfg.cls)}>
        <Icon className="w-3.5 h-3.5" /> {cfg.label}
      </span>
    )
  }

  const load = async () => {
    setLoading(true)
    try {
      const [base, reqRes] = await Promise.all([
        getModemsApi(),
        isAdmin ? Promise.resolve({ data: [] }) : mySimRequestsApi(),
      ])
      const details = await Promise.all(
        base.data.map(m => getModemDetailApi(m.id).then(r => r.data).catch(() => ({
          ...m, sms_sent: 0, sms_received: 0, sms_today: 0,
        } as Row)))
      )
      setRows(details)
      setMyRequests(reqRes.data)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  useEffect(() => {
    if (modems.length === 0) return
    setRows(prev => prev.map(r => {
      const live = modems.find(m => m.id === r.id)
      if (!live) return r
      return { ...r, signal_quality: live.signal_quality, status: live.status as Row['status'], operator: live.operator }
    }))
  }, [modems])

  const handleRefresh = async () => {
    setRefreshing(true)
    await load()
    setRefreshing(false)
  }

  const connected = rows.filter(r => (r.status === 'connected' && !r.vowifi_airplane) || (r.vowifi_mode && r.vowifi_running)).length

  const SignalBar = ({ quality }: { quality: number }) => {
    const bars = Math.round((quality / 100) * 5)
    const color = bars >= 4 ? 'bg-green-400' : bars >= 2 ? 'bg-yellow-400' : 'bg-red-400'
    return (
      <div className="flex items-end gap-0.5 h-4">
        {[1,2,3,4,5].map(i => (
          <div key={i} className={clsx('w-1.5 rounded-sm', i <= bars ? color : 'bg-gray-600')}
            style={{ height: `${i * 20}%` }} />
        ))}
        <span className="ml-1 text-xs text-gray-300">{quality}%</span>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-5">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white">{t('sim_title')}</h1>
          <p className="text-sm text-gray-400 mt-0.5">
            {t('all')} {rows.length} {t('sim_count')} · {t('sim_online')} <span className="text-green-400">{connected}</span>
          </p>
        </div>
        <button
          onClick={handleRefresh}
          disabled={refreshing}
          className="flex items-center gap-1.5 px-3 py-2 bg-gray-800 hover:bg-gray-700 text-gray-300 rounded-lg text-sm transition-colors disabled:opacity-50"
        >
          <RefreshCw className={clsx('w-4 h-4', refreshing && 'animate-spin')} />
          {t('refresh')}
        </button>
      </div>

      <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
        {[
          { label: t('sim_stat_total'), value: rows.length, color: 'text-blue-400' },
          { label: t('sim_stat_online'), value: connected, color: 'text-green-400' },
          { label: t('sim_stat_sms_today'), value: rows.reduce((a, r) => a + r.sms_today, 0), color: 'text-orange-400' },
          { label: t('sim_stat_upload'), value: fmtBytes(rows.reduce((a, r) => a + (r.tx_bytes ?? 0), 0)), color: 'text-purple-400' },
        ].map(s => (
          <div key={s.label} className="bg-gray-800 rounded-xl p-4 border border-gray-700">
            <p className="text-xs text-gray-400">{s.label}</p>
            <p className={clsx('text-2xl font-bold mt-1', s.color)}>{s.value}</p>
          </div>
        ))}
      </div>

      {loading ? (
        <div className="flex items-center gap-2 text-gray-400 py-10 justify-center">
          <RefreshCw className="w-4 h-4 animate-spin" /> {t('loading')}
        </div>
      ) : rows.length === 0 ? (
        <div className="bg-gray-800 border border-dashed border-gray-600 rounded-xl p-12 text-center space-y-3">
          {isAdmin ? (
            <p className="text-gray-500">{t('sim_no_device')}</p>
          ) : (
            <>
              <p className="text-gray-400">{t('sim_no_auth')}</p>
              <p className="text-gray-600 text-sm">{t('sim_go_resources')}</p>
              <a href="/resources" className="inline-block mt-1 text-sm text-blue-400 hover:text-blue-300">
                {t('sim_go_resources_link')}
              </a>
            </>
          )}
        </div>
      ) : (
        <div className="overflow-x-auto rounded-xl border border-gray-700">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-gray-800 text-gray-400 text-xs uppercase tracking-wider">
                {[
                  t('sim_col_sim'), t('sim_col_status'), t('sim_col_operator'), t('sim_col_tech'),
                  t('sim_col_reg'), t('sim_col_signal'), t('sim_col_up'), t('sim_col_down'),
                  t('sim_col_duration'), t('sim_col_sent'), t('sim_col_recv'), t('sim_col_today'), '',
                ].map((h, i) => (
                  <th key={i} className="px-4 py-3 text-left font-medium whitespace-nowrap">{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr
                  key={r.id}
                  onClick={() => navigate(`/modems/${r.id}`)}
                  className={clsx(
                    'border-t border-gray-700 cursor-pointer transition-colors hover:bg-gray-700',
                    i % 2 === 0 ? 'row-even bg-gray-900' : 'row-odd bg-gray-850'
                  )}
                >
                  <td className="px-4 py-3 whitespace-nowrap">
                    <div className="font-medium text-white">{r.alias || `SIM ${r.id}`}</div>
                    <div className="text-xs text-gray-500 font-mono mt-0.5">{r.phone_number || r.imei || r.device_path || t('none')}</div>
                  </td>
                  <td className="px-4 py-3 whitespace-nowrap"><StatusBadge status={r.status} vowifiOnline={r.vowifi_mode && r.vowifi_running} airplane={r.vowifi_airplane} /></td>
                  <td className="px-4 py-3 whitespace-nowrap text-gray-200">{r.vowifi_airplane ? <span className="text-gray-500">{t('none')}</span> : (r.operator || t('none'))}</td>
                  <td className="px-4 py-3 whitespace-nowrap">
                    {r.vowifi_airplane ? <span className="text-gray-500">{t('none')}</span> : (
                      <span className="px-1.5 py-0.5 rounded bg-blue-500/20 text-blue-300 text-xs font-mono">
                        {techLabel(r.access_technologies)}
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-3 whitespace-nowrap text-gray-300">{r.vowifi_airplane ? <span className="text-amber-400">{t('detail_reg_airplane')}</span> : regLabel(r.registration_state)}</td>
                  <td className="px-4 py-3">{r.vowifi_airplane ? <span className="text-gray-500">{t('none')}</span> : <SignalBar quality={r.signal_quality} />}</td>
                  <td className="px-4 py-3 whitespace-nowrap text-orange-300">
                    <span className="flex items-center gap-1"><Upload className="w-3 h-3" />{fmtBytes(r.tx_bytes)}</span>
                  </td>
                  <td className="px-4 py-3 whitespace-nowrap text-purple-300">
                    <span className="flex items-center gap-1"><Download className="w-3 h-3" />{fmtBytes(r.rx_bytes)}</span>
                  </td>
                  <td className="px-4 py-3 whitespace-nowrap text-gray-300">{fmtDuration(r.connection_duration, t)}</td>
                  <td className="px-4 py-3 whitespace-nowrap text-center text-gray-200">{r.sms_sent}</td>
                  <td className="px-4 py-3 whitespace-nowrap text-center text-gray-200">{r.sms_received}</td>
                  <td className="px-4 py-3 whitespace-nowrap text-center">
                    <span className={clsx('px-2 py-0.5 rounded-full text-xs font-medium',
                      r.sms_today > 0 ? 'bg-orange-500/20 text-orange-300' : 'text-gray-500')}>
                      {r.sms_today}
                    </span>
                  </td>
                  <td className="px-4 py-3 whitespace-nowrap" onClick={e => e.stopPropagation()}>
                    {(() => {
                      const status = modemAccessStatus(r.id)
                      if (status === 'access') return <ChevronRight className="w-4 h-4 text-gray-500" onClick={() => navigate(`/modems/${r.id}`)} />
                      if (status === 'pending') return (
                        <span className="inline-flex items-center gap-1 text-xs text-yellow-400 bg-yellow-400/10 px-2 py-1 rounded-full">
                          <Clock className="w-3 h-3" /> {t('sim_status_pending')}
                        </span>
                      )
                      if (status === 'approved') return (
                        <span className="inline-flex items-center gap-1 text-xs text-green-400 bg-green-400/10 px-2 py-1 rounded-full">
                          <CheckCircle className="w-3 h-3" /> {t('sim_status_approved')}
                        </span>
                      )
                      return (
                        <button
                          onClick={e => applyForModem(e, r.id)}
                          disabled={applying === r.id}
                          className="inline-flex items-center gap-1 text-xs text-blue-400 bg-blue-400/10 hover:bg-blue-400/20 px-2 py-1 rounded-full transition-colors disabled:opacity-50"
                        >
                          <ClipboardList className="w-3 h-3" />
                          {applying === r.id ? t('sim_status_applying') : t('sim_status_apply')}
                        </button>
                      )
                    })()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
