import { useEffect, useState } from 'react'
import { Cpu, Wifi, WifiOff } from 'lucide-react'
import {
  LineChart, Line, BarChart, Bar, PieChart, Pie, Cell,
  XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Legend,
} from 'recharts'
import { useModemStore } from '../store/modemStore'
import { getModemsApi } from '../api/modems'
import { getDashboardStatsApi, type DashboardStats } from '../api/dashboard'
import { useT } from '../i18n'

const SENT_COLOR = '#3b82f6'
const FAIL_COLOR = '#ef4444'
const PIE_COLORS = ['#10b981', '#ef4444', '#94a3b8']

const TASK_KEYS = ['active', 'completed', 'failed', 'paused'] as const
const TASK_COLORS: Record<string, string> = { active: '#3b82f6', completed: '#10b981', failed: '#ef4444', paused: '#f59e0b' }

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <h2 className="text-xs font-medium text-blue-200/50 uppercase tracking-widest mb-3">
      {children}
    </h2>
  )
}

function ChartCard({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <div className="rounded-2xl p-5" style={{ background: 'var(--card-bg)', border: '1px solid rgba(59,130,246,0.12)' }}>
      <div className="mb-4">
        <p className="text-sm font-medium" style={{ color: 'var(--text-primary)' }}>{title}</p>
        {subtitle && <p className="text-xs mt-0.5" style={{ color: 'var(--text-secondary)' }}>{subtitle}</p>}
      </div>
      {children}
    </div>
  )
}

const CustomTooltip = ({ active, payload, label }: any) => {
  if (!active || !payload?.length) return null
  return (
    <div className="rounded-xl px-3 py-2 text-xs shadow-lg"
      style={{ background: 'var(--surface-2)', border: '1px solid var(--border-default)' }}>
      <p className="mb-1" style={{ color: 'var(--text-secondary)' }}>{label}</p>
      {payload.map((p: any) => (
        <p key={p.dataKey} style={{ color: p.color }}>
          {p.name}：{p.value}
        </p>
      ))}
    </div>
  )
}

export default function Dashboard() {
  const { modems } = useModemStore()
  const t = useT()
  const [stats, setStats] = useState<DashboardStats | null>(null)

  const taskLabels: Record<string, string> = {
    active: t('dash_task_active'),
    completed: t('dash_task_completed'),
    failed: t('dash_task_failed'),
    paused: t('dash_task_paused'),
  }

  const connected = modems.filter(m => m.status === 'connected').length
  const total = modems.length
  const offline = total - connected

  useEffect(() => {
    getModemsApi()
    getDashboardStatsApi().then(r => setStats(r.data)).catch(() => {})
  }, [])

  const statCards = [
    { label: t('dash_total'),   value: total,     icon: Cpu,    color: '#3b82f6',  border: 'rgba(59,130,246,0.2)',  glow: 'rgba(59,130,246,0.12)' },
    { label: t('dash_online'),  value: connected, icon: Wifi,   color: '#10b981',  border: 'rgba(16,185,129,0.25)', glow: 'rgba(16,185,129,0.08)' },
    { label: t('dash_offline'), value: offline,   icon: WifiOff, color: '#94a3b8', border: 'rgba(148,163,184,0.15)', glow: 'rgba(148,163,184,0.06)' },
  ]

  // Pie data for month SMS
  const pieData = stats ? [
    { name: t('dash_rate_sent'),    value: stats.month_sms.sent },
    { name: t('dash_rate_failed'),  value: stats.month_sms.failed },
    { name: t('dash_rate_pending'), value: stats.month_sms.pending },
  ] : []

  // Signal bar chart data from live modem store
  const signalData = modems.map(m => ({
    name: m.alias || `SIM ${m.id}`,
    signal: m.signal_quality,
    fill: m.signal_quality >= 70 ? '#10b981' : m.signal_quality >= 40 ? '#f59e0b' : '#ef4444',
  }))

  // Task bar data
  const taskData = stats
    ? TASK_KEYS.map(k => ({ name: taskLabels[k], value: stats.tasks[k], fill: TASK_COLORS[k] }))
    : []

  return (
    <div className="p-6 space-y-6 animate-fade-up">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold text-white text-glow">{t('dash_title')}</h1>
        <p className="text-sm text-blue-300/60 mt-0.5">
          {connected > 0
            ? <><span className="inline-block w-1.5 h-1.5 rounded-full bg-emerald-400 mr-1.5 align-middle" style={{ boxShadow: '0 0 6px #34d399' }} />{connected} {t('dash_online')}</>
            : <span className="text-slate-500">{t('dash_no_online')}</span>}
        </p>
      </div>

      {/* Stat cards */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        {statCards.map(s => {
          const Icon = s.icon
          return (
            <div key={s.label}
              className="stat-card card-surface relative overflow-hidden rounded-2xl p-5 transition-all duration-300 hover:-translate-y-0.5"
              style={{ background: 'var(--card-bg)', backdropFilter: 'blur(20px)', border: `1px solid ${s.border}`, boxShadow: `0 0 32px ${s.glow}, inset 0 1px 0 rgba(255,255,255,0.04)` }}>
              <div className="absolute inset-0 animate-shimmer pointer-events-none" />
              <div className="flex items-start justify-between">
                <div>
                  <p className="stat-label text-xs text-blue-200/50 uppercase tracking-wider">{s.label}</p>
                  <p className="text-4xl font-bold mt-2" style={{ color: s.color, textShadow: `0 0 20px ${s.glow}` }}>
                    {s.value}
                  </p>
                </div>
                <div className="p-2.5 rounded-xl" style={{ background: s.glow, border: `1px solid ${s.border}` }}>
                  <Icon className="w-5 h-5" style={{ color: s.color }} />
                </div>
              </div>
            </div>
          )
        })}
      </div>

      {/* Charts row 1: SMS trend + Success rate */}
      <div>
        <SectionTitle>{t('dash_sms_section')}</SectionTitle>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          {/* SMS trend - spans 2 cols */}
          <div className="col-span-2">
            <ChartCard title={t('dash_trend_title')} subtitle={t('dash_trend_subtitle')}>
              <div style={{ display: 'flex', gap: 16, marginBottom: 12 }}>
                <span style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: 12, color: 'var(--text-secondary)' }}>
                  <span style={{ width: 20, height: 2, background: SENT_COLOR, display: 'inline-block', borderRadius: 1 }} />{t('dash_trend_sent')}
                </span>
                <span style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: 12, color: 'var(--text-secondary)' }}>
                  <span style={{ width: 20, height: 2, background: FAIL_COLOR, display: 'inline-block', borderRadius: 1, borderTop: `2px dashed ${FAIL_COLOR}` }} />{t('dash_trend_failed')}
                </span>
              </div>
              <ResponsiveContainer width="100%" height={180}>
                <LineChart data={stats?.sms_trend ?? []} margin={{ top: 4, right: 8, bottom: 0, left: -16 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="rgba(59,130,246,0.08)" />
                  <XAxis dataKey="date" tick={{ fontSize: 11, fill: '#64748b' }}
                    tickFormatter={v => v.slice(5)} />
                  <YAxis tick={{ fontSize: 11, fill: '#64748b' }} allowDecimals={false} />
                  <Tooltip content={<CustomTooltip />} />
                  <Line type="monotone" dataKey="sent" name={t('dash_trend_sent')} stroke={SENT_COLOR} strokeWidth={2} dot={{ r: 3, fill: SENT_COLOR }} activeDot={{ r: 4 }} />
                  <Line type="monotone" dataKey="failed" name={t('dash_trend_failed')} stroke={FAIL_COLOR} strokeWidth={2} strokeDasharray="4 3" dot={{ r: 3, fill: FAIL_COLOR }} activeDot={{ r: 4 }} />
                </LineChart>
              </ResponsiveContainer>
            </ChartCard>
          </div>

          {/* Donut: success rate */}
          <ChartCard title={t('dash_rate_title')} subtitle={t('dash_rate_subtitle')}>
            <ResponsiveContainer width="100%" height={140}>
              <PieChart>
                <Pie data={pieData} cx="50%" cy="50%" innerRadius={42} outerRadius={62}
                  dataKey="value" paddingAngle={2} startAngle={90} endAngle={-270}>
                  {pieData.map((_, i) => <Cell key={i} fill={PIE_COLORS[i]} />)}
                </Pie>
                <Tooltip content={<CustomTooltip />} />
              </PieChart>
            </ResponsiveContainer>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '6px 12px', justifyContent: 'center', marginTop: 4 }}>
              {pieData.map((d, i) => (
                <span key={d.name} style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 11, color: 'var(--text-secondary)' }}>
                  <span style={{ width: 8, height: 8, borderRadius: 2, background: PIE_COLORS[i], display: 'inline-block' }} />
                  {d.name} {d.value}
                </span>
              ))}
            </div>
          </ChartCard>
        </div>
      </div>

      {/* Charts row 2: Task status + Signal */}
      <div>
        <SectionTitle>{t('dash_device_section')}</SectionTitle>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          {/* Task status */}
          <ChartCard title={t('dash_task_title')} subtitle={t('dash_task_subtitle')}>
            <ResponsiveContainer width="100%" height={150}>
              <BarChart data={taskData} layout="vertical" margin={{ top: 0, right: 16, bottom: 0, left: 16 }}>
                <XAxis type="number" tick={{ fontSize: 11, fill: '#64748b' }} allowDecimals={false} />
                <YAxis type="category" dataKey="name" tick={{ fontSize: 12, fill: '#64748b' }} width={32} />
                <Tooltip content={<CustomTooltip />} />
                <Bar dataKey="value" name={t('dash_task_count')} radius={[0, 4, 4, 0]} maxBarSize={18}>
                  {taskData.map((d, i) => <Cell key={i} fill={d.fill} />)}
                </Bar>
              </BarChart>
            </ResponsiveContainer>
          </ChartCard>

          {/* Signal quality */}
          <ChartCard title={t('dash_signal_title')} subtitle={t('dash_signal_subtitle')}>
            <ResponsiveContainer width="100%" height={150}>
              <BarChart data={signalData} margin={{ top: 0, right: 8, bottom: 0, left: -16 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="rgba(59,130,246,0.08)" vertical={false} />
                <XAxis dataKey="name" tick={{ fontSize: 10, fill: '#64748b' }} />
                <YAxis domain={[0, 100]} tick={{ fontSize: 11, fill: '#64748b' }} />
                <Tooltip content={<CustomTooltip />} />
                <Bar dataKey="signal" name={t('dash_signal_label')} radius={[4, 4, 0, 0]} maxBarSize={28}>
                  {signalData.map((d, i) => <Cell key={i} fill={d.fill} />)}
                </Bar>
              </BarChart>
            </ResponsiveContainer>
          </ChartCard>
        </div>
      </div>
    </div>
  )
}
