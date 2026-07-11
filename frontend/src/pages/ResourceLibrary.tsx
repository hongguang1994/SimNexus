import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { Wifi, WifiOff, AlertCircle, HelpCircle, RefreshCw, CheckCircle, Clock, X, Lock } from 'lucide-react'
import clsx from 'clsx'
import { getAvailableModemsApi, type Modem } from '../api/modems'
import { mySimRequestsApi, myGrantsApi, createSimRequestApi, type SimAccessRequest, type SimGrant, type PermissionLevel } from '../api/simRequests'
import { useAuthStore } from '../store/authStore'
import { useT } from '../i18n'

type AccessStatus = 'use' | 'view' | 'pending' | 'none' | 'expired'

// ApproverScope: null = not an approver, 'all' = unrestricted approver, Set = managed modem IDs
type ApproverScope = Set<number> | 'all' | null

/** Derive approver scope from RBAC roles */
function getApproverScope(roles: any[]): ApproverScope {
  const approverRoles = roles.filter(r => r.can_approve_requests)
  if (approverRoles.length === 0) return null
  if (approverRoles.some(r => r.allowed_modem_ids == null)) return 'all'
  const ids = new Set<number>()
  approverRoles.forEach(r => (r.allowed_modem_ids ?? []).forEach((id: number) => ids.add(id)))
  return ids
}

/** IDs auto-granted by non-approver roles that have explicit allowed_modem_ids */
function getRoleGrantedIds(roles: any[]): Set<number> {
  const ids = new Set<number>()
  roles.filter(r => !r.can_approve_requests && r.allowed_modem_ids != null)
    .forEach(r => (r.allowed_modem_ids as number[]).forEach(id => ids.add(id)))
  return ids
}

/** Effective status for the current user — single source of truth */
function getEffectiveStatus(
  modemId: number,
  isAdmin: boolean,
  approverScope: ApproverScope,
  roleGrantedIds: Set<number>,
  grants: SimGrant[],
  requests: SimAccessRequest[]
): AccessStatus {
  if (isAdmin) return 'use'
  if (approverScope === 'all' || (approverScope instanceof Set && approverScope.has(modemId))) return 'use'
  if (roleGrantedIds.has(modemId)) return 'use'

  // Check sim_grants (authoritative grant record)
  const now = new Date()
  const grant = grants.find(g => g.modem_id === modemId)
  if (grant) {
    if (grant.expires_at && new Date(grant.expires_at) <= now) return 'expired'
    return grant.granted_level === 'use' ? 'use' : 'view'
  }

  // Check request status for pending indicator
  if (requests.find(r => r.modem_id === modemId && r.status === 'pending')) return 'pending'
  return 'none'
}

function ApplyModal({ modem, onClose, onDone }: { modem: Modem; onClose: () => void; onDone: () => void }) {
  const t = useT()
  const [level, setLevel] = useState<PermissionLevel>('use')
  const [reason, setReason] = useState('')
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')

  const submit = async () => {
    setLoading(true); setErr('')
    try {
      await createSimRequestApi(modem.id, reason || undefined, level)
      onDone(); onClose()
    } catch (e: any) {
      setErr(e.response?.data?.detail || t('rlib_modal_submit_failed'))
    } finally { setLoading(false) }
  }

  return createPortal(
    <div className="fixed inset-0 bg-black/70 flex items-center justify-center p-4" style={{ zIndex: 9999 }} onClick={onClose}>
      <div
        className="w-full max-w-md rounded-2xl overflow-hidden"
        style={{ background: 'var(--surface-2)', border: '1px solid var(--border-default)', boxShadow: '0 0 40px rgba(59,130,246,0.2), 0 25px 60px rgba(0,0,0,0.5)' }}
        onClick={e => e.stopPropagation()}>
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4" style={{ borderBottom: '1px solid var(--border-subtle)', background: 'var(--surface-header)' }}>
          <div>
            <h2 className="text-base font-semibold text-white">{t('rlib_modal_title')}</h2>
            <p className="text-xs text-blue-300/60 mt-0.5">
              {modem.alias || `SIM ${modem.id}`}{modem.operator ? ` — ${modem.operator}` : ''}
            </p>
          </div>
          <button onClick={onClose} className="w-7 h-7 flex items-center justify-center rounded-lg text-gray-500 hover:text-white hover:bg-white/10 transition-colors">
            <X className="w-4 h-4" />
          </button>
        </div>
        {/* Body */}
        <div className="p-6 space-y-4">

        <div>
          <p className="text-xs text-blue-200/50 uppercase tracking-wider mb-2">{t('rlib_modal_level_label')}</p>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            {([
              ['use', t('rlib_modal_level_use_title'), t('rlib_modal_level_use_desc')],
              ['view', t('rlib_modal_level_view_title'), t('rlib_modal_level_view_desc')],
            ] as const).map(([v, title, desc]) => (
              <button key={v} onClick={() => setLevel(v)}
                className={clsx('p-3 rounded-xl border text-left transition-all', level === v
                  ? 'border-blue-500 bg-blue-500/15 shadow-[0_0_12px_rgba(59,130,246,0.2)]'
                  : 'border-blue-500/15 bg-white/3 hover:border-blue-500/40')}>
                <p className="text-sm font-medium text-white">{title}</p>
                <p className="text-xs text-gray-400 mt-0.5">{desc}</p>
              </button>
            ))}
          </div>
        </div>

        <div>
          <label className="block text-xs text-blue-200/50 uppercase tracking-wider mb-1.5">{t('rlib_modal_reason_label')}</label>
          <textarea
            value={reason} onChange={e => setReason(e.target.value)} rows={2}
            placeholder={t('rlib_modal_reason_ph')}
            className="w-full rounded-xl px-3 py-2 text-sm text-white resize-none focus:outline-none focus:border-blue-500/60 placeholder-gray-600"
            style={{ background: 'var(--surface-input)', border: '1px solid var(--border-input)' }}
          />
        </div>

        {err && <p className="text-red-400 text-xs">{err}</p>}

        <div className="flex gap-3 justify-end pt-1">
          <button onClick={onClose} className="px-4 py-2 text-sm text-gray-400 hover:text-white">{t('rlib_modal_cancel')}</button>
          <button onClick={submit} disabled={loading}
            className="px-5 py-2 rounded-xl text-sm font-medium text-white transition-all disabled:opacity-50"
            style={{ background: 'linear-gradient(135deg,#3b82f6,#6366f1)', boxShadow: '0 0 16px rgba(59,130,246,0.35)' }}>
            {loading ? t('rlib_modal_submitting') : t('rlib_modal_submit')}
          </button>
        </div>
        </div>{/* end body */}
      </div>
    </div>,
    document.body
  )
}

export default function ResourceLibrary() {
  const t = useT()
  const { user } = useAuthStore()
  const isAdmin = user?.role === 'admin'
  const [modems, setModems] = useState<Modem[]>([])
  const [grants, setGrants] = useState<SimGrant[]>([])
  const [requests, setRequests] = useState<SimAccessRequest[]>([])
  const [loading, setLoading] = useState(true)
  const [applyTarget, setApplyTarget] = useState<Modem | null>(null)

  const approverScope = isAdmin ? null : getApproverScope(user?.rbac_roles ?? [])
  const roleGrantedIds = isAdmin ? new Set<number>() : getRoleGrantedIds(user?.rbac_roles ?? [])

  const STATUS_CFG = {
    connected:    { icon: Wifi,        color: 'text-emerald-400', dot: '#34d399', label: t('status_connected') },
    disconnected: { icon: WifiOff,     color: 'text-slate-500',   dot: '#64748b', label: t('status_disconnected') },
    error:        { icon: AlertCircle, color: 'text-red-400',     dot: '#f87171', label: t('status_error') },
    unknown:      { icon: HelpCircle,  color: 'text-amber-400',   dot: '#fbbf24', label: t('status_unknown') },
  } as const

  const ACCESS_BADGE: Record<AccessStatus, { label: string; cls: string }> = {
    use:     { label: t('rlib_access_use'),     cls: 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/30' },
    view:    { label: t('rlib_access_view'),    cls: 'bg-blue-500/15 text-blue-300 border border-blue-500/30' },
    pending: { label: t('rlib_access_pending'), cls: 'bg-amber-500/15 text-amber-300 border border-amber-500/30' },
    expired: { label: t('rlib_access_expired'), cls: 'bg-gray-600/30 text-gray-400 border border-gray-600/30' },
    none:    { label: t('rlib_access_none'),    cls: 'bg-red-500/10 text-red-400 border border-red-500/20' },
  }

  const load = async () => {
    setLoading(true)
    try {
      const [mRes, gRes, rRes] = await Promise.all([
        getAvailableModemsApi(),
        isAdmin ? Promise.resolve({ data: [] as SimGrant[] }) : myGrantsApi(),
        isAdmin ? Promise.resolve({ data: [] as SimAccessRequest[] }) : mySimRequestsApi(),
      ])
      setModems(mRes.data)
      setGrants(gRes.data)
      setRequests(rRes.data)
    } finally { setLoading(false) }
  }

  useEffect(() => { load() }, [])

  return (
    <div className="p-6 space-y-6 animate-fade-up">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white text-glow">{t('rlib_title')}</h1>
          <p className="text-sm text-blue-200/50 mt-0.5">{t('rlib_subtitle')}</p>
        </div>
        <button onClick={load} disabled={loading}
          className="flex items-center gap-1.5 px-3 py-2 rounded-lg text-sm text-blue-300/70 hover:text-blue-200 transition-colors disabled:opacity-50"
          style={{ background: 'rgba(59,130,246,0.08)', border: '1px solid rgba(59,130,246,0.15)' }}>
          <RefreshCw className={clsx('w-4 h-4', loading && 'animate-spin')} /> {t('rlib_refresh')}
        </button>
      </div>

      {loading ? (
        <div className="flex items-center gap-2 text-blue-300/40 py-16 justify-center">
          <RefreshCw className="w-4 h-4 animate-spin" /> {t('rlib_loading')}
        </div>
      ) : modems.length === 0 ? (
        <div className="rounded-2xl p-16 text-center border border-dashed border-blue-500/15 text-blue-300/30">
          {t('rlib_empty')}
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
          {modems.map(m => {
            const effectiveStatus = getEffectiveStatus(m.id, isAdmin, approverScope, roleGrantedIds, grants, requests)
            const badge = ACCESS_BADGE[effectiveStatus]
            const modemCfg = STATUS_CFG[m.status as keyof typeof STATUS_CFG] ?? STATUS_CFG.unknown
            const Icon = modemCfg.icon
            const canApply = !isAdmin && (effectiveStatus === 'none' || effectiveStatus === 'expired')

            return (
              <div key={m.id}
                className="sim-card relative overflow-hidden rounded-2xl p-4 transition-all duration-300 hover:-translate-y-0.5 group"
                style={{
                  background: 'var(--card-bg)',
                  backdropFilter: 'blur(20px)',
                  border: `1px solid ${effectiveStatus === 'use' ? 'rgba(52,211,153,0.2)' : effectiveStatus === 'pending' ? 'rgba(251,191,36,0.15)' : 'rgba(59,130,246,0.12)'}`,
                  boxShadow: effectiveStatus === 'use' ? '0 0 20px rgba(52,211,153,0.1)' : '0 0 20px rgba(59,130,246,0.06)',
                }}>
                {/* top glow line — hidden in light theme via .sim-card-glow */}
                <div className="sim-card-glow absolute top-0 left-4 right-4 h-px opacity-40"
                  style={{ background: `linear-gradient(90deg,transparent,${modemCfg.dot},transparent)` }} />

                {/* Header */}
                <div className="flex items-start justify-between mb-3">
                  <div className="flex-1 min-w-0">
                    <h3 className="sim-card-name font-semibold text-white truncate group-hover:text-blue-200 transition-colors">
                      {m.alias || `SIM ${m.id}`}
                    </h3>
                    <p className="sim-card-sub text-xs text-blue-200/30 mt-0.5 truncate">{m.operator || t('rlib_unknown_op')}</p>
                  </div>
                  <div className="flex flex-col items-end gap-1 ml-2 shrink-0">
                    <Icon className={`w-4 h-4 ${modemCfg.color}`} />
                    <span className="text-[10px]" style={{ color: modemCfg.dot }}>{modemCfg.label}</span>
                  </div>
                </div>

                {/* Info */}
                <div className="space-y-1.5 text-xs mb-3">
                  <div className="flex justify-between">
                    <span className="sim-card-label text-blue-200/40">{t('rlib_phone_label')}</span>
                    <span className="sim-card-value text-blue-100/70">{m.phone_number || '—'}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className="sim-card-label text-blue-200/40">{t('rlib_signal_label')}</span>
                    <div className="flex items-end gap-0.5 h-3">
                      {[1,2,3,4,5].map(i => {
                        const bars = Math.round((m.signal_quality / 100) * 5)
                        const colors = ['#f87171','#fb923c','#facc15','#4ade80','#34d399']
                        return <div key={i} className="w-1 rounded-sm"
                          style={{ height: `${i * 20}%`, background: i <= bars ? (colors[bars-1] ?? '#34d399') : 'var(--card-signal-empty)' }} />
                      })}
                    </div>
                  </div>
                </div>

                {/* Access badge + action */}
                <div className="flex items-center justify-between">
                  <span className={clsx('text-[11px] px-2 py-0.5 rounded-full', badge.cls)}>{badge.label}</span>
                  {canApply && (
                    <button onClick={() => setApplyTarget(m)}
                      className="sim-apply-btn text-xs px-3 py-1 rounded-lg text-blue-300 transition-all hover:text-white"
                      style={{ background: 'rgba(59,130,246,0.15)', border: '1px solid rgba(59,130,246,0.3)' }}>
                      {t('rlib_apply_btn')}
                    </button>
                  )}
                  {effectiveStatus === 'pending' && (
                    <span className="sim-status-wait flex items-center gap-1 text-xs text-amber-400/70">
                      <Clock className="w-3 h-3" /> {t('rlib_wait_approve')}
                    </span>
                  )}
                  {(effectiveStatus === 'use' || effectiveStatus === 'view') && (
                    <span className="sim-status-ok flex items-center gap-1 text-xs text-emerald-400/70">
                      <CheckCircle className="w-3 h-3" /> {t('rlib_can_use')}
                    </span>
                  )}
                  {isAdmin && (
                    <span className="sim-status-admin flex items-center gap-1 text-xs text-blue-400/60">
                      <Lock className="w-3 h-3" /> {t('rlib_admin_badge')}
                    </span>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {applyTarget && (
        <ApplyModal modem={applyTarget} onClose={() => setApplyTarget(null)} onDone={load} />
      )}
    </div>
  )
}
