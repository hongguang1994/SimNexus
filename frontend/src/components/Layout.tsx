import { NavLink, Outlet, useNavigate, useLocation } from 'react-router-dom'
import {
  LayoutDashboard, Send, Clock, MessageSquare, Cpu, CreditCard,
  Users, LogOut, Sun, Moon, Monitor, ChevronDown, User, KeyRound, X, ShieldCheck,
  Wifi, RefreshCw, ArrowUp, MessageCircle, PanelLeftClose, PanelLeftOpen,
  Bell, WifiOff, AlertTriangle, UserPlus, CheckCheck, Activity, Shield, FileText, ClipboardCheck, Database, Bot, ScrollText,
} from 'lucide-react'
import { useState, useRef, useEffect } from 'react'
import { useModemStore } from '../store/modemStore'
import { useModemSocket } from '../hooks/useModemSocket'
import { useAuthStore } from '../store/authStore'
import { useThemeStore, type ThemeMode } from '../store/themeStore'
import { useLangStore, type Lang } from '../store/langStore'
import { useT } from '../i18n'
import { format } from 'date-fns'
import { changePasswordApi, getMeApi } from '../api/auth'
import { getUnreadApi } from '../api/support'
import {
  getNotificationsApi, getUnreadCountApi, markAllReadApi, markOneReadApi,
  type AppNotification,
} from '../api/notifications'
import SupportChat from './SupportChat'
import clsx from 'clsx'

const THEME_OPTS: { mode: ThemeMode; icon: typeof Sun; key: 'nav_theme_light' | 'nav_theme_system' | 'nav_theme_dark' }[] = [
  { mode: 'light',  icon: Sun,     key: 'nav_theme_light'  },
  { mode: 'system', icon: Monitor, key: 'nav_theme_system' },
  { mode: 'dark',   icon: Moon,    key: 'nav_theme_dark'   },
]

const LANG_OPTS: { lang: Lang; flag: string; label: string }[] = [
  { lang: 'zh', flag: '🇨🇳', label: '中文' },
  { lang: 'en', flag: '🇬🇧', label: 'English' },
]

function Dropdown({ trigger, children }: { trigger: React.ReactNode; children: React.ReactNode }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const handler = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false) }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])
  return (
    <div ref={ref} className="relative">
      <div onClick={() => setOpen(v => !v)} className="cursor-pointer">{trigger}</div>
      {open && (
        <div
          className="absolute right-0 top-full mt-2 z-50 min-w-[180px] bg-gray-800 border border-gray-700 rounded-xl shadow-2xl py-1"
          onClick={() => setOpen(false)}
        >
          {children}
        </div>
      )}
    </div>
  )
}

function DropdownItem({ onClick, children, active, danger }: { onClick: () => void; children: React.ReactNode; active?: boolean; danger?: boolean }) {
  return (
    <button onClick={onClick}
      className={clsx('w-full flex items-center gap-2.5 px-4 py-2 text-sm transition-colors text-left',
        danger ? 'text-red-400 hover:bg-red-500/10'
        : active ? 'text-blue-400 bg-blue-500/10'
        : 'text-gray-300 hover:text-white hover:bg-gray-700')}>
      {children}
    </button>
  )
}

function DropdownDivider() {
  return <div className="my-1 border-t border-gray-700" />
}

// ── Profile modal ─────────────────────────────────────────────────────────────

function ProfileModal({ onClose, initialTab = "info" }: { onClose: () => void; initialTab?: "info" | "pwd" }) {
  const t = useT()
  const lang = useLangStore(s => s.lang)
  const { user, setAuth } = useAuthStore()
  const [tab, setTab] = useState<'info' | 'pwd'>(initialTab)
  const [oldPwd, setOldPwd] = useState('')
  const [newPwd, setNewPwd] = useState('')
  const [confirmPwd, setConfirmPwd] = useState('')
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')
  const [ok, setOk] = useState(false)

  const inputCls = "w-full bg-gray-900 border border-gray-700 rounded-lg px-3 py-2.5 text-white text-sm focus:outline-none focus:border-blue-500 transition-colors"

  const handleChangePwd = async (e: React.FormEvent) => {
    e.preventDefault()
    setErr('')
    if (newPwd !== confirmPwd) {
      setErr(t('layout_pwd_mismatch'))
      return
    }
    if (newPwd.length < 6) {
      setErr(t('layout_pwd_too_short'))
      return
    }
    setSaving(true)
    try {
      await changePasswordApi(oldPwd, newPwd)
      setOk(true)
      setOldPwd(''); setNewPwd(''); setConfirmPwd('')
    } catch (err: any) {
      setErr(err.response?.data?.detail || t('change_fail'))
    } finally {
      setSaving(false)
    }
  }

  const fmtDate = (iso: string) =>
    new Date(iso).toLocaleString(lang === 'zh' ? 'zh-CN' : 'en-US')

  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4" onClick={onClose}>
      <div className="bg-gray-800 border border-gray-700 rounded-2xl shadow-2xl w-full max-w-md" onClick={e => e.stopPropagation()}>
        {/* Header */}
        <div className="flex items-center justify-between px-6 pt-5 pb-4 border-b border-gray-700">
          <h2 className="text-lg font-semibold text-white">
            {t('layout_profile_title')}
          </h2>
          <button onClick={onClose} className="text-gray-500 hover:text-gray-300 transition-colors">
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Tabs */}
        <div className="flex border-b border-gray-700">
          {([['info', t('layout_profile_tab')], ['pwd', t('layout_pwd_tab')]] as const).map(([id, label]) => (
            <button key={id} onClick={() => { setTab(id); setErr(''); setOk(false) }}
              className={clsx('flex-1 py-2.5 text-sm font-medium transition-colors',
                tab === id ? 'text-blue-400 border-b-2 border-blue-400' : 'text-gray-400 hover:text-gray-300')}>
              {label}
            </button>
          ))}
        </div>

        <div className="p-6">
          {tab === 'info' && (
            <div className="space-y-1">
              {/* Avatar */}
              <div className="flex flex-col items-center py-4">
                <div className="w-16 h-16 rounded-full bg-blue-600 flex items-center justify-center text-white text-2xl font-bold mb-3">
                  {user?.username?.[0]?.toUpperCase()}
                </div>
                <p className="text-white font-semibold text-lg">{user?.username}</p>
                <span className={clsx(
                  'mt-1 inline-flex items-center gap-1 px-2.5 py-0.5 rounded-full text-xs font-medium',
                  user?.role === 'admin' ? 'bg-blue-500/20 text-blue-300' : 'bg-gray-600/50 text-gray-400'
                )}>
                  <ShieldCheck className="w-3 h-3" />
                  {user?.role === 'admin'
                    ? t('nav_admin')
                    : user?.rbac_roles?.length
                    ? user.rbac_roles.map((r: any) => r.name).join(' · ')
                    : t('nav_user')}
                </span>
              </div>

              <div className="bg-gray-900/60 rounded-xl overflow-hidden">
                {[
                  { label: t('layout_username_label'), value: user?.username },
                  { label: t('layout_role_label'), value: user?.role === 'admin' ? t('nav_admin') : user?.rbac_roles?.length ? user.rbac_roles.map((r: any) => r.name).join(' · ') : t('nav_user') },
                  { label: t('layout_status_label'), value: user?.is_active ? t('layout_status_active') : t('layout_status_disabled') },
                  { label: t('layout_created_label'), value: user?.created_at ? fmtDate(user.created_at) : '—' },
                ].map(({ label, value }, i) => (
                  <div key={i} className={clsx('flex items-center justify-between px-4 py-3 text-sm', i !== 0 && 'border-t border-gray-700/60')}>
                    <span className="text-gray-400">{label}</span>
                    <span className="text-gray-100 font-medium">{value}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {tab === 'pwd' && (
            <form onSubmit={handleChangePwd} className="space-y-4">
              <div>
                <label className="block text-xs text-gray-400 mb-1.5">{t('change_old_pwd')}</label>
                <input type="password" value={oldPwd} onChange={e => setOldPwd(e.target.value)}
                  required autoFocus placeholder={t('change_old_pwd_ph')} className={inputCls} />
              </div>
              <div>
                <label className="block text-xs text-gray-400 mb-1.5">{t('change_new_pwd')}</label>
                <input type="password" value={newPwd} onChange={e => setNewPwd(e.target.value)}
                  required placeholder={t('change_new_pwd_ph')} className={inputCls} />
              </div>
              <div>
                <label className="block text-xs text-gray-400 mb-1.5">
                  {t('layout_confirm_pwd')}
                </label>
                <input type="password" value={confirmPwd} onChange={e => setConfirmPwd(e.target.value)}
                  required placeholder={t('layout_confirm_pwd_ph')} className={inputCls} />
              </div>

              {err && <p className="text-red-400 text-sm bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">{err}</p>}
              {ok && <p className="text-green-400 text-sm bg-green-400/10 border border-green-400/20 rounded-lg px-3 py-2">
                {t('layout_pwd_success')}
              </p>}

              <button type="submit" disabled={saving}
                className="w-full bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white rounded-lg py-2.5 text-sm font-medium transition-colors">
                {saving ? t('layout_pwd_saving') : t('change_submit')}
              </button>
            </form>
          )}
        </div>
      </div>
    </div>
  )
}


// ── Collapsible nav group ─────────────────────────────────────────────────────

function NavGroup({
  label, routes, sideCollapsed, children,
}: {
  label: string
  routes: string[]
  sideCollapsed: boolean
  children: React.ReactNode
}) {
  const location = useLocation()
  const isAnyActive = routes.some(r =>
    r === '/' ? location.pathname === '/' : location.pathname.startsWith(r)
  )
  const [open, setOpen] = useState(isAnyActive)

  // Auto-expand when navigating into this group
  useEffect(() => {
    if (isAnyActive) setOpen(true)
  }, [location.pathname]) // eslint-disable-line react-hooks/exhaustive-deps

  // Collapsed sidebar: show items as icon strip without group header
  if (sideCollapsed) {
    return <div className="space-y-0.5 py-1">{children}</div>
  }

  return (
    <div className="mb-0.5">
      <button
        onClick={() => setOpen(v => !v)}
        className="w-full flex items-center justify-between px-3.5 py-1.5 rounded-full text-xs font-medium text-blue-300/40 hover:text-blue-200/60 uppercase tracking-widest transition-colors"
      >
        {label}
        <ChevronDown className={clsx('w-3 h-3 transition-transform duration-200 shrink-0', open && 'rotate-180')} />
      </button>
      <div
        className="overflow-hidden transition-[max-height] duration-200 ease-in-out"
        style={{ maxHeight: open ? '480px' : '0px' }}
      >
        <div className="space-y-0.5 pl-2">{children}</div>
      </div>
    </div>
  )
}

// ── Floating sidebar ──────────────────────────────────────────────────────────

function FloatBtn({ icon: Icon, label, onClick, color = 'text-blue-400', badge }: {
  icon: typeof Send; label: string; onClick: () => void; color?: string; badge?: number
}) {
  const [hovered, setHovered] = useState(false)
  return (
    <div className="relative flex items-center justify-end"
      onMouseEnter={() => setHovered(true)} onMouseLeave={() => setHovered(false)}>
      {hovered && (
        <div className="absolute right-14 bg-gray-800 border border-gray-700 text-white text-xs rounded-lg px-3 py-1.5 whitespace-nowrap shadow-lg pointer-events-none">
          {label}
          <div className="absolute right-[-5px] top-1/2 -translate-y-1/2 w-2 h-2 bg-gray-800 border-r border-t border-gray-700 rotate-45" />
        </div>
      )}
      <button onClick={onClick}
        className="w-10 h-10 bg-gray-800 border border-gray-700 rounded-xl flex flex-col items-center justify-center shadow-md hover:border-blue-500 hover:shadow-blue-500/20 hover:shadow-lg transition-all relative group">
        <Icon className={clsx('w-4 h-4', color)} />
        {badge != null && badge > 0 && (
          <span className="absolute -top-1.5 -right-1.5 w-4 h-4 bg-green-500 rounded-full text-white text-[9px] font-bold flex items-center justify-center">
            {badge > 9 ? '9+' : badge}
          </span>
        )}
      </button>
    </div>
  )
}

// ── Layout ────────────────────────────────────────────────────────────────────

export default function Layout() {
  useModemSocket()
  const modems = useModemStore(s => s.modems)
  const { user, clearAuth, perm, canSupport, canApprove, setAuth, token } = useAuthStore()
  const p = perm()

  // Refresh user info on mount so that role/permission changes take effect without re-login
  useEffect(() => {
    if (!token) return
    getMeApi().then(r => setAuth(token, r.data)).catch(() => {})
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const navigate = useNavigate()
  const location = useLocation()
  const { mode, setMode } = useThemeStore()
  const { lang, setLang } = useLangStore()
  const t = useT()
  const [profileTab, setProfileTab] = useState<'info' | 'pwd'>('info')
  const [showProfile, setShowProfile] = useState(false)
  const [showSupport, setShowSupport] = useState(false)
  const [supportUnread, setSupportUnread] = useState(0)
  const [sideCollapsed, setSideCollapsed] = useState(false)
  const [mobileNav, setMobileNav] = useState(false) // 手机上侧边栏抽屉是否展开
  const [isMobile, setIsMobile] = useState(false)
  useEffect(() => {
    const mq = window.matchMedia('(max-width: 767px)')
    const on = () => { setIsMobile(mq.matches); if (mq.matches) setSideCollapsed(false) }
    on()
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  useEffect(() => { setMobileNav(false) }, [location.pathname]) // 切换路由后自动收起抽屉
  const [showNotif, setShowNotif] = useState(false)
  const [notifUnread, setNotifUnread] = useState(0)
  const [notifs, setNotifs] = useState<AppNotification[]>([])
  const notifRef = useRef<HTMLDivElement>(null)

  const scrollToTop = () => {
    document.querySelector('main')?.scrollTo({ top: 0, behavior: 'smooth' })
  }

  useEffect(() => {
    const poll = () => getUnreadApi().then(r => setSupportUnread(r.data.count)).catch(() => {})
    poll()
    const t = setInterval(poll, 10000)
    return () => clearInterval(t)
  }, [])

  // Notification polling (all users)
  useEffect(() => {
    const poll = () => getUnreadCountApi().then(r => setNotifUnread(r.data.count)).catch(() => {})
    poll()
    const t = setInterval(poll, 15000)
    return () => clearInterval(t)
  }, [])

  // Close notification panel on outside click
  useEffect(() => {
    if (!showNotif) return
    const handler = (e: MouseEvent) => {
      if (notifRef.current && !notifRef.current.contains(e.target as Node)) setShowNotif(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [showNotif])

  const openNotif = async () => {
    if (!showNotif) {
      const res = await getNotificationsApi().catch(() => null)
      if (res) setNotifs(res.data)
    }
    setShowNotif(v => !v)
  }

  const markAllRead = async () => {
    await markAllReadApi()
    setNotifs(prev => prev.map(n => ({ ...n, is_read: true })))
    setNotifUnread(0)
  }

  const markOneRead = async (id: number) => {
    await markOneReadApi(id)
    setNotifs(prev => prev.map(n => n.id === id ? { ...n, is_read: true } : n))
    setNotifUnread(prev => Math.max(0, prev - 1))
  }

  const NOTIF_ICON: Record<string, { icon: typeof Bell; color: string }> = {
    modem_online:   { icon: Wifi,          color: 'text-green-400'  },
    modem_offline:  { icon: WifiOff,       color: 'text-red-400'    },
    sms_failed:     { icon: AlertTriangle, color: 'text-orange-400' },
    task_failed:    { icon: AlertTriangle, color: 'text-orange-400' },
    new_user:       { icon: UserPlus,      color: 'text-blue-400'   },
    support_msg:    { icon: MessageCircle, color: 'text-purple-400' },
    support_reply:  { icon: MessageCircle, color: 'text-blue-400'   },
  }

  const logout = () => { clearAuth(); navigate('/login') }

  const currentLang = LANG_OPTS.find(l => l.lang === lang)!
  const currentThemeOpt = THEME_OPTS.find(o => o.mode === mode)!

  const navLinkCls = ({ isActive }: { isActive: boolean }) =>
    clsx('flex items-center gap-3 px-3.5 py-2.5 rounded-full text-sm transition-all duration-200',
      sideCollapsed && 'justify-center px-0',
      isActive ? 'nav-active' : 'text-gray-400 hover:text-blue-200 hover:bg-blue-500/10')

  return (
    <div className="layout-root h-[100dvh] flex flex-col p-2 md:p-3 gap-2 md:gap-3 overflow-hidden">
      {/* Ambient background orbs — hidden in light theme via CSS */}
      <div className="layout-orbs fixed inset-0 pointer-events-none overflow-hidden z-0" aria-hidden>
        <div className="animate-orb absolute w-[600px] h-[600px] rounded-full opacity-30"
          style={{ background: 'radial-gradient(circle, rgba(59,130,246,0.35) 0%, transparent 70%)', top: '-150px', left: '-100px' }} />
        <div className="animate-orb-2 absolute w-[500px] h-[500px] rounded-full opacity-20"
          style={{ background: 'radial-gradient(circle, rgba(99,102,241,0.4) 0%, transparent 70%)', bottom: '-100px', right: '-100px' }} />
        <div className="animate-orb-3 absolute w-[400px] h-[400px] rounded-full opacity-15"
          style={{ background: 'radial-gradient(circle, rgba(6,182,212,0.3) 0%, transparent 70%)', top: '40%', right: '25%' }} />
      </div>
      {/* Top header */}
      <header className="glass-strong rounded-2xl border border-blue-500/10 shadow-xl shadow-black/20 h-14 flex items-center px-3 md:px-5 shrink-0 z-40">
        <div className="flex items-center gap-2 md:w-48 shrink-0">
          <button
            onClick={() => (isMobile ? setMobileNav(o => !o) : setSideCollapsed(c => !c))}
            className="p-1.5 rounded-lg text-gray-400 hover:text-white hover:bg-gray-700/60 transition-colors"
          >
            {(isMobile ? mobileNav : !sideCollapsed)
              ? <PanelLeftClose className="w-4 h-4" />
              : <PanelLeftOpen className="w-4 h-4" />
            }
          </button>
          <Cpu className="w-5 h-5 text-blue-400" />
          <span className="font-bold text-white">SimNexus</span>
        </div>

        <div className="flex-1" />

        <div className="flex items-center gap-1">
          {/* Notification bell */}
          {user && (
            <div className="relative" ref={notifRef}>
              <button
                onClick={openNotif}
                className="relative flex items-center justify-center w-9 h-9 rounded-lg text-gray-300 hover:text-white hover:bg-gray-700/60 transition-colors"
              >
                <Bell className="w-4 h-4" />
                {notifUnread > 0 && (
                  <span className="absolute top-1 right-1 w-4 h-4 bg-red-500 rounded-full text-white text-[9px] font-bold flex items-center justify-center leading-none">
                    {notifUnread > 9 ? '9+' : notifUnread}
                  </span>
                )}
              </button>

              {/* Notification panel */}
              {showNotif && (
                <div className="absolute right-0 top-11 w-[360px] bg-gray-800 border border-gray-700 rounded-xl shadow-2xl z-50 flex flex-col max-h-[480px]">
                  {/* Header */}
                  <div className="flex items-center justify-between px-4 py-3 border-b border-gray-700 shrink-0">
                    <span className="font-semibold text-white text-sm">{t('notif_title')}</span>
                    <button
                      onClick={markAllRead}
                      className="flex items-center gap-1.5 text-xs text-gray-400 hover:text-blue-400 transition-colors"
                    >
                      <CheckCheck className="w-3.5 h-3.5" />
                      {t('notif_mark_all')}
                    </button>
                  </div>

                  {/* List */}
                  <div className="overflow-y-auto flex-1">
                    {notifs.length === 0 ? (
                      <div className="flex flex-col items-center justify-center py-12 text-gray-500">
                        <Bell className="w-8 h-8 mb-2 opacity-20" />
                        <p className="text-xs">{t('notif_empty')}</p>
                      </div>
                    ) : notifs.map(n => {
                      const cfg = NOTIF_ICON[n.type] ?? { icon: Bell, color: 'text-gray-400' }
                      const Icon = cfg.icon
                      return (
                        <button
                          key={n.id}
                          onClick={() => !n.is_read && markOneRead(n.id)}
                          className={clsx(
                            'w-full flex items-start gap-3 px-4 py-3 text-left border-b border-gray-700/50 hover:bg-gray-700/40 transition-colors',
                            !n.is_read && 'bg-gray-700/20'
                          )}
                        >
                          <div className={clsx('mt-0.5 shrink-0', cfg.color)}>
                            <Icon className="w-4 h-4" />
                          </div>
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center justify-between gap-2">
                              <p className={clsx('text-sm font-medium truncate', n.is_read ? 'text-gray-300' : 'text-white')}>
                                {n.title}
                              </p>
                              {!n.is_read && <span className="w-1.5 h-1.5 rounded-full bg-blue-400 shrink-0" />}
                            </div>
                            {n.body && <p className="text-xs text-gray-400 mt-0.5 truncate">{n.body}</p>}
                            <p className="text-[10px] text-gray-500 mt-1">
                              {format(new Date(n.created_at), 'MM-dd HH:mm')}
                            </p>
                          </div>
                        </button>
                      )
                    })}
                  </div>
                </div>
              )}
            </div>
          )}

          {/* Language */}
          <Dropdown trigger={
            <div className="flex items-center gap-1.5 px-2 sm:px-3 py-1.5 rounded-lg text-sm text-gray-300 hover:text-white hover:bg-gray-700/60 transition-colors">
              <span className="text-base leading-none">{currentLang.flag}</span>
              <span className="hidden sm:block">{currentLang.label}</span>
              <ChevronDown className="w-3.5 h-3.5 text-gray-500 hidden sm:block" />
            </div>
          }>
            {LANG_OPTS.map(({ lang: l, flag, label }) => (
              <DropdownItem key={l} onClick={() => setLang(l)} active={lang === l}>
                <span className="text-base leading-none">{flag}</span> {label}
              </DropdownItem>
            ))}
          </Dropdown>

          {/* Theme */}
          <Dropdown trigger={
            <div className="flex items-center gap-1.5 px-2 sm:px-3 py-1.5 rounded-lg text-sm text-gray-300 hover:text-white hover:bg-gray-700/60 transition-colors">
              <currentThemeOpt.icon className="w-4 h-4" />
              <span className="hidden sm:block">{t(currentThemeOpt.key)}</span>
              <ChevronDown className="w-3.5 h-3.5 text-gray-500 hidden sm:block" />
            </div>
          }>
            {THEME_OPTS.map(({ mode: m, icon: Icon, key }) => (
              <DropdownItem key={m} onClick={() => setMode(m)} active={mode === m}>
                <Icon className="w-4 h-4" /> {t(key)}
              </DropdownItem>
            ))}
          </Dropdown>

          <div className="w-px h-5 bg-gray-700 mx-1" />

          {/* User menu */}
          <Dropdown trigger={
            <div className="flex items-center gap-2 px-2 sm:px-3 py-1.5 rounded-lg text-sm text-gray-300 hover:text-white hover:bg-gray-700/60 transition-colors">
              <div className="w-7 h-7 rounded-full bg-blue-600 flex items-center justify-center text-white text-xs font-bold shrink-0">
                {user?.username?.[0]?.toUpperCase()}
              </div>
              <div className="hidden sm:block text-left">
                <p className="text-sm font-medium text-white leading-none">{user?.username}</p>
                <p className="text-xs text-gray-500 leading-none mt-0.5">
                  {user?.role === 'admin'
                    ? t('nav_admin')
                    : user?.rbac_roles?.length
                    ? user.rbac_roles.map((r: any) => r.name).join(' · ')
                    : t('nav_user')}
                </p>
              </div>
              <ChevronDown className="w-3.5 h-3.5 text-gray-500 hidden sm:block" />
            </div>
          }>
            {/* Header */}
            <div className="px-4 py-3 border-b border-gray-700">
              <div className="flex items-center gap-3">
                <div className="w-9 h-9 rounded-full bg-blue-600 flex items-center justify-center text-white text-sm font-bold shrink-0">
                  {user?.username?.[0]?.toUpperCase()}
                </div>
                <div>
                  <p className="text-sm font-medium text-white">{user?.username}</p>
                  <p className="text-xs text-gray-400">
                    {user?.role === 'admin'
                      ? t('nav_admin')
                      : user?.rbac_roles?.length
                      ? user.rbac_roles.map((r: any) => r.name).join(' · ')
                      : t('nav_user')}
                  </p>
                </div>
              </div>
            </div>

            <div className="py-1">
              <DropdownItem onClick={() => { setProfileTab('info'); setShowProfile(true) }}>
                <User className="w-4 h-4 text-gray-400" />
                {t('layout_profile')}
              </DropdownItem>
              <DropdownItem onClick={() => { setProfileTab('pwd'); setShowProfile(true) }}>
                <KeyRound className="w-4 h-4 text-gray-400" />
                {t('layout_change_pwd')}
              </DropdownItem>
            </div>

            <DropdownDivider />

            <div className="py-1">
              <DropdownItem onClick={logout} danger>
                <LogOut className="w-4 h-4" />
                {t('nav_logout')}
              </DropdownItem>
            </div>
          </Dropdown>
        </div>
      </header>

      {/* Body */}
      <div className="flex flex-1 min-h-0 gap-2 md:gap-3">
        {/* 手机上侧边栏抽屉的半透明背景遮罩 */}
        {mobileNav && (
          <div className="fixed inset-0 bg-black/50 z-40 md:hidden" onClick={() => setMobileNav(false)} />
        )}
        <aside className={clsx(
          'glass-strong rounded-2xl border border-blue-500/10 shadow-xl shadow-black/20 flex flex-col py-4 overflow-y-auto overflow-x-hidden transition-transform duration-200',
          // 手机(<md)：固定定位的抽屉，按 mobileNav 滑入/滑出
          'max-md:fixed max-md:inset-y-2 max-md:left-2 max-md:z-50 max-md:w-52 max-md:px-2',
          mobileNav ? 'max-md:translate-x-0' : 'max-md:-translate-x-[130%]',
          // 平板/桌面(>=md)：在流内、可折叠宽度
          'md:shrink-0 md:h-full md:translate-x-0',
          sideCollapsed ? 'md:w-14 md:px-1' : 'md:w-52 md:px-2'
        )}>
          <nav className="flex-1 space-y-1">

            {/* ── 总览（独立入口，不分组）─────────────────── */}
            <NavLink to="/" end className={navLinkCls} title={sideCollapsed ? t('nav_overview') : undefined}>
              <LayoutDashboard className="w-4 h-4 shrink-0" />
              {!sideCollapsed && <span>{t('nav_overview')}</span>}
            </NavLink>

            {/* ── SIM 资源 ───────────────────────────────── */}
            {(p.can_view_sim || canApprove()) && (
              <NavGroup
                label={t('layout_group_sim')}
                routes={['/resources', '/sim-cards', '/admin/sim-requests']}
                sideCollapsed={sideCollapsed}
              >
                {p.can_view_sim && (
                  <NavLink to="/resources" className={navLinkCls} title={sideCollapsed ? t('layout_resources') : undefined}>
                    <Database className="w-4 h-4 shrink-0" />
                    {!sideCollapsed && <span>{t('layout_resources')}</span>}
                  </NavLink>
                )}
                {p.can_view_sim && (
                  <NavLink to="/sim-cards" className={navLinkCls} title={sideCollapsed ? t('nav_sim') : undefined}>
                    <CreditCard className="w-4 h-4 shrink-0" />
                    {!sideCollapsed && <span>{t('nav_sim')}</span>}
                  </NavLink>
                )}
                {canApprove() && (
                  <NavLink to="/admin/sim-requests" className={navLinkCls} title={sideCollapsed ? t('layout_sim_requests') : undefined}>
                    <ClipboardCheck className="w-4 h-4 shrink-0" />
                    {!sideCollapsed && <span>{t('layout_sim_requests')}</span>}
                  </NavLink>
                )}
              </NavGroup>
            )}

            {/* ── 短信 ───────────────────────────────────── */}
            <NavGroup
              label={t('layout_group_sms')}
              routes={['/templates', '/history', '/tasks', '/admin/tasks']}
              sideCollapsed={sideCollapsed}
            >
              {!p.read_only && (
                <NavLink to="/templates" className={navLinkCls} title={sideCollapsed ? t('layout_templates') : undefined}>
                  <FileText className="w-4 h-4 shrink-0" />
                  {!sideCollapsed && <span>{t('layout_templates')}</span>}
                </NavLink>
              )}
              <NavLink to="/history" className={navLinkCls} title={sideCollapsed ? t('nav_history') : undefined}>
                <MessageSquare className="w-4 h-4 shrink-0" />
                {!sideCollapsed && <span>{t('nav_history')}</span>}
              </NavLink>
              {!p.read_only && (
                <NavLink to="/tasks" className={navLinkCls} title={sideCollapsed ? t('nav_tasks') : undefined}>
                  <Clock className="w-4 h-4 shrink-0" />
                  {!sideCollapsed && <span>{t('nav_tasks')}</span>}
                </NavLink>
              )}
              {(user?.role === 'admin' || !p.read_only) && (
                <NavLink to="/admin/tasks" className={navLinkCls} title={sideCollapsed ? t('nav_admin_tasks') : undefined}>
                  <Activity className="w-4 h-4 shrink-0" />
                  {!sideCollapsed && (
                    <span>{user?.role === 'admin' ? t('nav_admin_tasks') : t('layout_my_tasks')}</span>
                  )}
                </NavLink>
              )}
            </NavGroup>

            {/* ── 系统管理（仅管理员）──────────────────────── */}
            {user?.role === 'admin' && (
              <NavGroup
                label={t('layout_group_admin')}
                routes={['/users', '/roles', '/admin/telegram', '/admin/logs']}
                sideCollapsed={sideCollapsed}
              >
                <NavLink to="/users" className={navLinkCls} title={sideCollapsed ? t('nav_users') : undefined}>
                  <Users className="w-4 h-4 shrink-0" />
                  {!sideCollapsed && <span>{t('nav_users')}</span>}
                </NavLink>
                <NavLink to="/roles" className={navLinkCls} title={sideCollapsed ? t('layout_roles') : undefined}>
                  <Shield className="w-4 h-4 shrink-0" />
                  {!sideCollapsed && <span>{t('layout_roles')}</span>}
                </NavLink>
                <NavLink to="/admin/telegram" className={navLinkCls} title={sideCollapsed ? 'Telegram' : undefined}>
                  <Bot className="w-4 h-4 shrink-0" />
                  {!sideCollapsed && <span>Telegram</span>}
                </NavLink>
                <NavLink to="/admin/logs" className={navLinkCls} title={sideCollapsed ? t('nav_logs') : undefined}>
                  <ScrollText className="w-4 h-4 shrink-0" />
                  {!sideCollapsed && <span>{t('nav_logs')}</span>}
                </NavLink>
              </NavGroup>
            )}

            {/* ── 客服 ───────────────────────────────────── */}
            {canSupport() && (
              <NavGroup
                label={t('layout_group_support')}
                routes={['/support']}
                sideCollapsed={sideCollapsed}
              >
                <NavLink to="/support" className={navLinkCls} title={sideCollapsed ? t('nav_support') : undefined}>
                  <div className="relative shrink-0">
                    <MessageCircle className="w-4 h-4" />
                    {supportUnread > 0 && (
                      <span className="absolute -top-1 -right-1 w-3 h-3 bg-red-500 rounded-full text-white text-[8px] font-bold flex items-center justify-center leading-none">
                        {supportUnread > 9 ? '9' : supportUnread}
                      </span>
                    )}
                  </div>
                  {!sideCollapsed && (
                    <span className="flex-1 flex items-center justify-between">
                      {t('nav_support')}
                      {supportUnread > 0 && (
                        <span className="ml-auto bg-red-500 text-white text-[10px] font-bold px-1.5 py-0.5 rounded-full leading-none">
                          {supportUnread > 99 ? '99+' : supportUnread}
                        </span>
                      )}
                    </span>
                  )}
                </NavLink>
              </NavGroup>
            )}

          </nav>

          <div className={clsx('border-t border-gray-700 pt-3 mt-3', sideCollapsed ? 'px-0 flex justify-center' : 'px-3')}>
            {sideCollapsed ? (
              <span title={`${modems.filter(m => m.status === 'connected').length}/${modems.length}`}><Wifi className="w-4 h-4 text-green-400" /></span>
            ) : (
              <p className="text-xs text-gray-500">
                {t('nav_online')}: <span className="text-green-400">{modems.filter(m => m.status === 'connected').length}</span> / {modems.length}
              </p>
            )}
          </div>
        </aside>

        <main className="flex-1 min-w-0 overflow-auto">
          <Outlet />
        </main>
      </div>


      {/* Floating right sidebar — 仅在总览页显示 */}
      <div className={clsx('fixed right-4 bottom-8 z-30 flex flex-col gap-2.5', location.pathname !== '/' && 'hidden')}>
        <FloatBtn
          icon={Send}
          label={t('nav_history')}
          onClick={() => navigate('/history')}
          color="text-blue-400"
        />
        <FloatBtn
          icon={Wifi}
          label={t('nav_overview')}
          onClick={() => navigate('/')}
          color="text-green-400"
          badge={modems.filter(m => m.status === 'connected').length}
        />
        <FloatBtn
          icon={RefreshCw}
          label={t('layout_reload')}
          onClick={() => window.location.reload()}
          color="text-yellow-400"
        />
        <div className="w-10 h-px bg-gray-700" />
        <FloatBtn
          icon={ArrowUp}
          label={t('layout_back_to_top')}
          onClick={scrollToTop}
          color="text-gray-400"
        />
        <div className="w-10 h-px bg-gray-700" />
        <FloatBtn
          icon={MessageCircle}
          label={t('layout_online_chat')}
          onClick={() => setShowSupport(v => !v)}
          color="text-blue-400"
          badge={supportUnread}
        />
      </div>

      {showProfile && <ProfileModal onClose={() => setShowProfile(false)} initialTab={profileTab} />}
      {showSupport && <SupportChat onClose={() => setShowSupport(false)} onUnreadChange={setSupportUnread} />}
    </div>
  )
}
