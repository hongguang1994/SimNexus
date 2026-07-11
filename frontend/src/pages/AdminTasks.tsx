import { useEffect, useState, useCallback } from 'react'
import { format } from 'date-fns'
import {
  Play, Trash2, PauseCircle, ChevronDown, ChevronRight,
  CheckCircle, XCircle, Clock, Activity, Users, Search, RefreshCw,
} from 'lucide-react'
import clsx from 'clsx'
import {
  adminGetTasksApi, adminGetTaskStatsApi, adminGetTaskHistoryApi,
  updateTaskApi, deleteTaskApi, runTaskNowApi,
  type ScheduledTask, type TaskStats, type SmsMessage,
} from '../api/sms'
import { listUsersApi, type UserOut } from '../api/auth'
import { useT } from '../i18n'
import { useAuthStore } from '../store/authStore'

// ── Stat card ─────────────────────────────────────────────────────────────────

function StatCard({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div className="bg-gray-800 border border-gray-700 rounded-xl px-5 py-4">
      <p className="text-xs text-gray-400 mb-1">{label}</p>
      <p className={clsx('text-2xl font-bold', color)}>{value}</p>
    </div>
  )
}

// ── Status badge ──────────────────────────────────────────────────────────────

const STATUS_CLS: Record<string, string> = {
  active:    'bg-green-900/60 text-green-300',
  paused:    'bg-yellow-900/60 text-yellow-300',
  completed: 'bg-gray-700 text-gray-400',
  failed:    'bg-red-900/60 text-red-300',
}

// ── History drawer ────────────────────────────────────────────────────────────

function HistoryRow({ msg }: { msg: SmsMessage }) {
  return (
    <div className="flex items-center gap-3 py-2 border-b border-gray-700/50 text-xs">
      {msg.status === 'sent'
        ? <CheckCircle className="w-3.5 h-3.5 text-green-400 shrink-0" />
        : <XCircle className="w-3.5 h-3.5 text-red-400 shrink-0" />
      }
      <span className="text-gray-300 w-32 shrink-0">{msg.phone_number}</span>
      <span className={clsx('shrink-0', msg.status === 'sent' ? 'text-green-400' : 'text-red-400')}>
        {msg.status}
      </span>
      {msg.error_message && (
        <span className="text-red-400 truncate max-w-[200px]">{msg.error_message}</span>
      )}
      <span className="ml-auto text-gray-500 shrink-0">
        {msg.created_at ? format(new Date(msg.created_at), 'MM-dd HH:mm') : '—'}
      </span>
    </div>
  )
}

// ── Task row ──────────────────────────────────────────────────────────────────

function TaskRow({
  task, onRefresh, t,
}: { task: ScheduledTask; onRefresh: () => void; t: ReturnType<typeof useT> }) {
  const [expanded, setExpanded] = useState(false)
  const [history, setHistory] = useState<SmsMessage[]>([])
  const [loadingHist, setLoadingHist] = useState(false)

  const STATUS_LABEL: Record<string, string> = {
    active: t('atask_status_active'),
    paused: t('atask_status_paused'),
    completed: t('atask_status_completed'),
    failed: t('atask_status_failed'),
  }

  const loadHistory = async () => {
    if (expanded) { setExpanded(false); return }
    setExpanded(true)
    setLoadingHist(true)
    adminGetTaskHistoryApi(task.id).then(r => setHistory(r.data)).finally(() => setLoadingHist(false))
  }

  const toggle = async () => {
    const newStatus = task.status === 'active' ? 'paused' : 'active'
    await updateTaskApi(task.id, { status: newStatus } as any)
    onRefresh()
  }

  const remove = async () => {
    if (!confirm(`${t('atask_delete_confirm')}「${task.name}」？`)) return
    await deleteTaskApi(task.id)
    onRefresh()
  }

  const runNow = async () => {
    await runTaskNowApi(task.id)
    setTimeout(onRefresh, 1200)
  }

  return (
    <>
      <tr className={clsx('border-b border-gray-700/50 hover:bg-gray-750 transition-colors', expanded && 'bg-gray-800/60')}>
        {/* Expand */}
        <td className="px-3 py-3">
          <button onClick={loadHistory} className="text-gray-500 hover:text-gray-300">
            {expanded ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
          </button>
        </td>
        {/* Name */}
        <td className="px-3 py-3">
          <p className="text-sm font-medium text-white">{task.name}</p>
          <p className="text-xs text-gray-500 mt-0.5 truncate max-w-[180px]">{task.content}</p>
        </td>
        {/* Creator */}
        <td className="px-3 py-3">
          {task.created_by_username ? (
            <span className="inline-flex items-center gap-1.5 text-xs text-gray-300">
              <div className="w-5 h-5 rounded-full bg-indigo-600 flex items-center justify-center text-white text-[9px] font-bold shrink-0">
                {task.created_by_username[0].toUpperCase()}
              </div>
              {task.created_by_username}
            </span>
          ) : <span className="text-gray-600 text-xs">—</span>}
        </td>
        {/* Schedule */}
        <td className="px-3 py-3 text-xs text-gray-400">
          {task.cron_expression
            ? <span className="font-mono">{task.cron_expression}</span>
            : task.send_once_at
              ? format(new Date(task.send_once_at), 'MM-dd HH:mm')
              : '—'}
        </td>
        {/* Status */}
        <td className="px-3 py-3">
          <span className={clsx('text-xs px-2 py-0.5 rounded-full font-medium', STATUS_CLS[task.status])}>
            {STATUS_LABEL[task.status] ?? task.status}
          </span>
        </td>
        {/* Recipients */}
        <td className="px-3 py-3 text-xs text-gray-400 text-center">{task.recipients.length}</td>
        {/* Run count */}
        <td className="px-3 py-3 text-xs text-gray-300 text-center">{task.run_count}</td>
        {/* Last run */}
        <td className="px-3 py-3 text-xs text-gray-400">
          {task.last_run_at ? format(new Date(task.last_run_at), 'MM-dd HH:mm') : '—'}
        </td>
        {/* Next run */}
        <td className="px-3 py-3 text-xs text-gray-400">
          {task.next_run_at ? format(new Date(task.next_run_at), 'MM-dd HH:mm') : '—'}
        </td>
        {/* Actions */}
        <td className="px-3 py-3">
          <div className="flex items-center gap-1.5">
            <button onClick={runNow} title={t('atask_run_now')}
              className="text-gray-400 hover:text-green-400 transition-colors">
              <Play className="w-3.5 h-3.5" />
            </button>
            {(task.status === 'active' || task.status === 'paused') && (
              <button onClick={toggle}
                title={task.status === 'active' ? t('atask_pause') : t('atask_resume')}
                className="text-gray-400 hover:text-yellow-400 transition-colors">
                <PauseCircle className="w-3.5 h-3.5" />
              </button>
            )}
            <button onClick={remove} title={t('atask_delete')}
              className="text-gray-400 hover:text-red-400 transition-colors">
              <Trash2 className="w-3.5 h-3.5" />
            </button>
          </div>
        </td>
      </tr>

      {/* History drawer */}
      {expanded && (
        <tr>
          <td colSpan={10} className="px-6 py-3 bg-gray-900/60">
            <p className="text-xs font-semibold text-gray-400 mb-2 uppercase tracking-wider">
              {t('atask_history_title')}
            </p>
            {loadingHist ? (
              <p className="text-xs text-gray-500">{t('atask_history_loading')}</p>
            ) : history.length === 0 ? (
              <p className="text-xs text-gray-600">{t('atask_history_empty')}</p>
            ) : history.map(m => <HistoryRow key={m.id} msg={m} />)}
          </td>
        </tr>
      )}
    </>
  )
}

// ── Main page ─────────────────────────────────────────────────────────────────

export default function AdminTasks() {
  const t = useT()
  const { user } = useAuthStore()
  const isAdmin = user?.role === 'admin'
  const [tasks, setTasks] = useState<ScheduledTask[]>([])
  const [stats, setStats] = useState<TaskStats | null>(null)
  const [users, setUsers] = useState<UserOut[]>([])
  const [filterStatus, setFilterStatus] = useState('')
  const [filterUser, setFilterUser] = useState<number | ''>('')
  const [search, setSearch] = useState('')
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    const [tasksRes, statsRes] = await Promise.all([
      adminGetTasksApi({ status: filterStatus || undefined, user_id: filterUser || undefined }),
      adminGetTaskStatsApi(),
    ])
    setTasks(tasksRes.data)
    setStats(statsRes.data)
    setLoading(false)
  }, [filterStatus, filterUser])

  useEffect(() => { load() }, [load])
  useEffect(() => { if (isAdmin) listUsersApi().then(r => setUsers(r.data)) }, [isAdmin])

  const filtered = tasks.filter(tk =>
    !search || tk.name.toLowerCase().includes(search.toLowerCase())
      || (tk.created_by_username ?? '').toLowerCase().includes(search.toLowerCase())
  )

  return (
    <div className="p-6 space-y-5">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold text-white flex items-center gap-2">
          <Activity className="w-6 h-6 text-blue-400" />
          {isAdmin ? t('atask_title_monitor') : t('atask_title_my')}
        </h1>
        <button onClick={load} className="flex items-center gap-1.5 text-sm text-gray-400 hover:text-white transition-colors">
          <RefreshCw className="w-4 h-4" /> {t('atask_refresh')}
        </button>
      </div>

      {/* Stat cards */}
      {stats && (
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
          <StatCard label={t('atask_stat_total')}     value={stats.total}     color="text-white" />
          <StatCard label={t('atask_stat_active')}    value={stats.active}    color="text-green-400" />
          <StatCard label={t('atask_stat_paused')}    value={stats.paused}    color="text-yellow-400" />
          <StatCard label={t('atask_stat_completed')} value={stats.completed} color="text-gray-400" />
          <StatCard label={t('atask_stat_failed')}    value={stats.failed}    color="text-red-400" />
        </div>
      )}

      {/* Filters */}
      <div className="flex items-center gap-3 flex-wrap">
        <div className="relative">
          <Search className="w-3.5 h-3.5 text-gray-500 absolute left-3 top-1/2 -translate-y-1/2" />
          <input
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder={t('atask_search_ph')}
            className="bg-gray-800 border border-gray-700 rounded-lg pl-8 pr-3 py-1.5 text-sm text-white placeholder-gray-500 focus:outline-none focus:border-blue-500 w-56"
          />
        </div>

        <select
          value={filterStatus}
          onChange={e => setFilterStatus(e.target.value)}
          className="bg-gray-800 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-white"
        >
          <option value="">{t('atask_filter_all_status')}</option>
          <option value="active">{t('atask_filter_active')}</option>
          <option value="paused">{t('atask_filter_paused')}</option>
          <option value="completed">{t('atask_filter_completed')}</option>
          <option value="failed">{t('atask_filter_failed')}</option>
        </select>

        {isAdmin && (
          <select
            value={filterUser}
            onChange={e => setFilterUser(e.target.value ? Number(e.target.value) : '')}
            className="bg-gray-800 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-white"
          >
            <option value="">{t('atask_filter_all_users')}</option>
            {users.map(u => <option key={u.id} value={u.id}>{u.username}</option>)}
          </select>
        )}

        <div className="flex items-center gap-1.5 text-xs text-gray-500 ml-auto">
          <Users className="w-3.5 h-3.5" />
          {t('all')} {filtered.length} {t('atask_count')}
        </div>
      </div>

      {/* Table */}
      <div className="bg-gray-800 border border-gray-700 rounded-xl overflow-hidden">
        {loading ? (
          <div className="p-10 text-center text-gray-500 text-sm">{t('atask_loading')}</div>
        ) : filtered.length === 0 ? (
          <div className="p-10 text-center text-gray-500 text-sm">{t('atask_empty')}</div>
        ) : (
          <div className="overflow-x-auto"><table className="w-full min-w-[720px] text-sm">
            <thead className="border-b border-gray-700 text-xs text-gray-400 uppercase tracking-wider">
              <tr>
                <th className="px-3 py-3 w-8" />
                <th className="px-3 py-3 text-left">{t('atask_col_name')}</th>
                <th className="px-3 py-3 text-left">{t('atask_col_creator')}</th>
                <th className="px-3 py-3 text-left">{t('atask_col_schedule')}</th>
                <th className="px-3 py-3 text-left">{t('atask_col_status')}</th>
                <th className="px-3 py-3 text-center">{t('atask_col_recipients')}</th>
                <th className="px-3 py-3 text-center">{t('atask_col_runs')}</th>
                <th className="px-3 py-3 text-left">{t('atask_col_last_run')}</th>
                <th className="px-3 py-3 text-left">{t('atask_col_next_run')}</th>
                <th className="px-3 py-3 text-left">{t('atask_col_actions')}</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map(task => (
                <TaskRow key={task.id} task={task} onRefresh={load} t={t} />
              ))}
            </tbody>
          </table></div>
        )}
      </div>
    </div>
  )
}
