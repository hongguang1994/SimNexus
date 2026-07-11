import { useEffect, useState, useCallback } from 'react'
import { format } from 'date-fns'
import { ArrowDownLeft, ArrowUpRight, Copy, Check, RefreshCw, MessageSquare, Trash2, X, Wifi, Radio } from 'lucide-react'
import { getMessagesApi, deleteMessageApi, batchDeleteMessagesApi, SmsMessage } from '../api/sms'
import { useModemStore } from '../store/modemStore'
import { useT } from '../i18n'
import clsx from 'clsx'
import { createPortal } from 'react-dom'

function fallbackCopy(text: string, done: () => void) {
  const el = document.createElement('textarea')
  el.value = text
  el.style.cssText = 'position:fixed;top:-999px;left:-999px'
  document.body.appendChild(el)
  el.focus()
  el.select()
  try { document.execCommand('copy'); done() } catch {}
  document.body.removeChild(el)
}

function doCopy(text: string, done: () => void) {
  if (navigator.clipboard) {
    navigator.clipboard.writeText(text).then(done).catch(() => fallbackCopy(text, done))
  } else {
    fallbackCopy(text, done)
  }
}

function ConfirmModal({ message, onConfirm, onCancel }: { message: string; onConfirm: () => void; onCancel: () => void }) {
  const t = useT()
  return createPortal(
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50" onClick={onCancel}>
      <div className="bg-gray-800 border border-gray-700 rounded-xl p-5 w-full max-w-sm mx-4 space-y-4" onClick={e => e.stopPropagation()}>
        <div className="flex items-center gap-3">
          <div className="w-9 h-9 rounded-full bg-red-500/15 flex items-center justify-center shrink-0">
            <Trash2 className="w-4 h-4 text-red-400" />
          </div>
          <p className="text-gray-200 text-sm">{message}</p>
        </div>
        <div className="flex justify-end gap-2">
          <button onClick={onCancel} className="px-4 py-1.5 bg-gray-700 hover:bg-gray-600 text-gray-300 rounded-lg text-sm">{t('cancel')}</button>
          <button onClick={onConfirm} className="px-4 py-1.5 bg-red-600 hover:bg-red-500 text-white rounded-lg text-sm">{t('hist_delete_confirm')}</button>
        </div>
      </div>
    </div>,
    document.body
  )
}

function ContentModal({ text, onClose }: { text: string; onClose: () => void }) {
  const t = useT()
  const [copied, setCopied] = useState(false)
  const copy = useCallback(() => {
    doCopy(text, () => { setCopied(true); setTimeout(() => setCopied(false), 1500) })
  }, [text])
  return createPortal(
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50" onClick={onClose}>
      <div className="bg-gray-800 border border-gray-700 rounded-xl p-5 w-full max-w-lg space-y-3 mx-4" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between">
          <span className="text-sm font-medium text-gray-300">{t('hist_content_title')}</span>
          <button onClick={copy} className="flex items-center gap-1.5 text-xs text-gray-400 hover:text-white transition-colors">
            {copied ? <><Check className="w-3.5 h-3.5 text-green-400" />{t('hist_copied')}</> : <><Copy className="w-3.5 h-3.5" />{t('hist_copy')}</>}
          </button>
        </div>
        <p className="text-gray-200 text-sm leading-relaxed whitespace-pre-wrap break-all bg-gray-900 rounded-lg p-3 max-h-60 overflow-y-auto">{text}</p>
        <div className="flex justify-end">
          <button onClick={onClose} className="px-4 py-1.5 bg-gray-700 hover:bg-gray-600 text-gray-300 rounded-lg text-sm">{t('close')}</button>
        </div>
      </div>
    </div>,
    document.body
  )
}

function StatusBadge({ status }: { status: string }) {
  const t = useT()
  const cfg: Record<string, { cls: string; label: string }> = {
    sent:     { cls: 'bg-green-500/15 text-green-400 border-green-500/30', label: t('hist_status_sent') },
    received: { cls: 'bg-blue-500/15 text-blue-400 border-blue-500/30',   label: t('hist_status_received') },
    failed:   { cls: 'bg-red-500/15 text-red-400 border-red-500/30',      label: t('hist_status_failed') },
    pending:  { cls: 'bg-yellow-500/15 text-yellow-400 border-yellow-500/30', label: t('hist_status_pending') },
  }
  const c = cfg[status] ?? { cls: 'bg-gray-500/15 text-gray-400 border-gray-500/30', label: status }
  return (
    <span className={clsx('inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium border', c.cls)}>
      {c.label}
    </span>
  )
}

// ChannelBadge 显示该条短信走的收发渠道：VoWiFi 或 蜂窝(ModemManager)。
function ChannelBadge({ channel, t }: { channel?: string; t: ReturnType<typeof useT> }) {
  if (channel === 'vowifi') {
    return (
      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium border bg-indigo-500/15 text-indigo-300 border-indigo-500/30" title="IMS over Wi-Fi">
        <Wifi className="w-3 h-3" />{t('hist_channel_vowifi')}
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium border bg-gray-500/15 text-gray-400 border-gray-500/30" title="ModemManager / mmcli">
      <Radio className="w-3 h-3" />{t('hist_channel_cellular')}
    </span>
  )
}

export default function SmsHistory() {
  const modems = useModemStore(s => s.modems)
  const t = useT()
  const [messages, setMessages] = useState<SmsMessage[]>([])
  const [filterModem, setFilterModem] = useState<number | ''>('')
  const [filterDir, setFilterDir] = useState<string>('')
  const [filterStatus, setFilterStatus] = useState<string>('')
  const [loading, setLoading] = useState(true)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [copiedId, setCopiedId] = useState<number | null>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [confirmDelete, setConfirmDelete] = useState<null | { ids: number[]; label: string }>(null)
  const [deleting, setDeleting] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    setSelected(new Set())
    getMessagesApi({
      modem_id: filterModem || undefined,
      direction: filterDir || undefined,
      limit: 200,
    })
      .then(r => setMessages(r.data))
      .finally(() => setLoading(false))
  }, [filterModem, filterDir])

  useEffect(() => { load() }, [load])

  const filtered = filterStatus
    ? messages.filter(m => m.status === filterStatus)
    : messages

  const copyMsg = (m: SmsMessage) => {
    doCopy(m.content, () => {
      setCopiedId(m.id)
      setTimeout(() => setCopiedId(null), 1500)
    })
  }

  const modemName = (id: number) => {
    const m = modems.find(m => m.id === id)
    return m ? (m.alias || `SIM ${m.id}`) : `SIM ${id}`
  }

  const allSelected = filtered.length > 0 && filtered.every(m => selected.has(m.id))
  const someSelected = selected.size > 0

  const toggleAll = () => {
    if (allSelected) {
      setSelected(new Set())
    } else {
      setSelected(new Set(filtered.map(m => m.id)))
    }
  }

  const toggleOne = (id: number) => {
    const s = new Set(selected)
    s.has(id) ? s.delete(id) : s.add(id)
    setSelected(s)
  }

  const askDelete = (ids: number[]) => {
    const label = ids.length === 1
      ? t('hist_delete_one')
      : t('hist_delete_many').replace('{n}', String(ids.length))
    setConfirmDelete({ ids, label })
  }

  const doDelete = async () => {
    if (!confirmDelete) return
    setDeleting(true)
    try {
      const { ids } = confirmDelete
      if (ids.length === 1) {
        await deleteMessageApi(ids[0])
      } else {
        await batchDeleteMessagesApi(ids)
      }
      setMessages(prev => prev.filter(m => !ids.includes(m.id)))
      setSelected(prev => { const s = new Set(prev); ids.forEach(id => s.delete(id)); return s })
      setConfirmDelete(null)
    } catch (e) {
      console.error('delete failed', e)
      setConfirmDelete(null)
    } finally {
      setDeleting(false)
    }
  }

  const stats = {
    total: messages.length,
    sent: messages.filter(m => m.status === 'sent').length,
    received: messages.filter(m => m.status === 'received').length,
    failed: messages.filter(m => m.status === 'failed').length,
  }

  return (
    <div className="p-6 space-y-5">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold text-white">{t('hist_title')}</h1>
        <button
          onClick={load}
          disabled={loading}
          className="flex items-center gap-1.5 px-3 py-1.5 bg-gray-800 hover:bg-gray-700 text-gray-300 rounded-lg text-sm transition-colors disabled:opacity-50"
        >
          <RefreshCw className={clsx('w-4 h-4', loading && 'animate-spin')} />
          {t('refresh')}
        </button>
      </div>

      {/* 统计卡片 */}
      <div className="grid grid-cols-4 gap-3">
        {[
          { label: t('hist_stat_total'),      value: stats.total,    cls: 'text-white' },
          { label: t('hist_status_sent'),     value: stats.sent,     cls: 'text-green-400' },
          { label: t('hist_status_received'), value: stats.received, cls: 'text-blue-400' },
          { label: t('hist_status_failed'),   value: stats.failed,   cls: 'text-red-400' },
        ].map(s => (
          <div key={s.label} className="bg-gray-800 border border-gray-700 rounded-xl p-4 text-center">
            <p className={clsx('text-2xl font-bold', s.cls)}>{s.value}</p>
            <p className="text-xs text-gray-400 mt-1">{s.label}</p>
          </div>
        ))}
      </div>

      {/* 筛选栏 */}
      <div className="flex flex-wrap gap-3 items-center">
        <select
          value={filterModem}
          onChange={e => setFilterModem(e.target.value ? Number(e.target.value) : '')}
          className="bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:border-blue-500"
        >
          <option value="">{t('hist_all_devices')}</option>
          {modems.map(m => <option key={m.id} value={m.id}>{m.alias || `SIM ${m.id}`}</option>)}
        </select>
        <select
          value={filterDir}
          onChange={e => setFilterDir(e.target.value)}
          className="bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:border-blue-500"
        >
          <option value="">{t('hist_all_dir')}</option>
          <option value="inbound">{t('hist_inbound')}</option>
          <option value="outbound">{t('hist_outbound')}</option>
        </select>
        <select
          value={filterStatus}
          onChange={e => setFilterStatus(e.target.value)}
          className="bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:border-blue-500"
        >
          <option value="">{t('hist_all_status')}</option>
          <option value="sent">{t('hist_status_sent')}</option>
          <option value="received">{t('hist_status_received')}</option>
          <option value="failed">{t('hist_status_failed')}</option>
          <option value="pending">{t('hist_status_pending')}</option>
        </select>

        <div className="ml-auto flex items-center gap-3">
          {someSelected && (
            <button
              onClick={() => askDelete([...selected])}
              className="flex items-center gap-1.5 px-3 py-1.5 bg-red-600/20 hover:bg-red-600/30 text-red-400 border border-red-500/30 rounded-lg text-sm transition-colors"
            >
              <Trash2 className="w-3.5 h-3.5" />
              {t('hist_delete_selected')} ({selected.size})
            </button>
          )}
          <span className="text-sm text-gray-500">{filtered.length} {t('hist_records')}</span>
        </div>
      </div>

      {/* 表格 */}
      <div className="bg-gray-800 border border-gray-700 rounded-xl overflow-hidden">
        {loading ? (
          <div className="p-10 text-center text-gray-500 flex flex-col items-center gap-2">
            <RefreshCw className="w-5 h-5 animate-spin" />
            {t('loading')}
          </div>
        ) : filtered.length === 0 ? (
          <div className="p-10 text-center text-gray-500 flex flex-col items-center gap-2">
            <MessageSquare className="w-8 h-8 opacity-30" />
            {t('hist_empty')}
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead className="border-b border-gray-700 bg-gray-800/80">
              <tr>
                <th className="px-4 py-3 w-10">
                  <input
                    type="checkbox"
                    checked={allSelected}
                    onChange={toggleAll}
                    className="rounded border-gray-600 bg-gray-700 text-blue-500 focus:ring-0 focus:ring-offset-0 cursor-pointer"
                  />
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold text-gray-400 uppercase tracking-wider w-20">{t('hist_col_dir')}</th>
                <th className="px-4 py-3 text-left text-xs font-semibold text-gray-400 uppercase tracking-wider">{t('hist_col_device')}</th>
                <th className="px-4 py-3 text-left text-xs font-semibold text-gray-400 uppercase tracking-wider w-24">{t('hist_col_channel')}</th>
                <th className="px-4 py-3 text-left text-xs font-semibold text-gray-400 uppercase tracking-wider">{t('hist_col_phone')}</th>
                <th className="px-4 py-3 text-left text-xs font-semibold text-gray-400 uppercase tracking-wider">{t('hist_col_content')}</th>
                <th className="px-4 py-3 text-left text-xs font-semibold text-gray-400 uppercase tracking-wider w-24">{t('hist_col_status')}</th>
                <th className="px-4 py-3 text-left text-xs font-semibold text-gray-400 uppercase tracking-wider w-32">{t('hist_col_time')}</th>
                <th className="px-4 py-3 w-10"></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-700/50">
              {filtered.map(m => (
                <tr key={m.id} className={clsx('hover:bg-gray-700/30 transition-colors group', selected.has(m.id) && 'bg-blue-500/5')}>
                  <td className="px-4 py-3">
                    <input
                      type="checkbox"
                      checked={selected.has(m.id)}
                      onChange={() => toggleOne(m.id)}
                      className="rounded border-gray-600 bg-gray-700 text-blue-500 focus:ring-0 focus:ring-offset-0 cursor-pointer"
                    />
                  </td>
                  <td className="px-4 py-3">
                    {m.direction === 'inbound' ? (
                      <span className="inline-flex items-center gap-1.5 text-blue-400 text-xs font-medium">
                        <ArrowDownLeft className="w-4 h-4" />{t('hist_inbound')}
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1.5 text-green-400 text-xs font-medium">
                        <ArrowUpRight className="w-4 h-4" />{t('hist_outbound')}
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-gray-400 text-xs">{modemName(m.modem_id)}</td>
                  <td className="px-4 py-3"><ChannelBadge channel={m.channel} t={t} /></td>
                  <td className="px-4 py-3 text-gray-200 font-mono text-xs">{m.phone_number}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-2 max-w-xs">
                      <span
                        className="truncate text-gray-300 cursor-pointer hover:text-white text-xs"
                        title={m.content}
                        onClick={() => setExpanded(m.content)}
                      >{m.content}</span>
                      <button
                        onClick={() => copyMsg(m)}
                        className="shrink-0 opacity-0 group-hover:opacity-100 transition-opacity text-gray-500 hover:text-gray-200"
                      >
                        {copiedId === m.id
                          ? <Check className="w-3.5 h-3.5 text-green-400" />
                          : <Copy className="w-3.5 h-3.5" />}
                      </button>
                    </div>
                  </td>
                  <td className="px-4 py-3"><StatusBadge status={m.status} /></td>
                  <td className="px-4 py-3 text-gray-400 text-xs whitespace-nowrap">
                    {format(new Date(m.created_at), 'MM-dd HH:mm')}
                  </td>
                  <td className="px-4 py-3">
                    <button
                      onClick={() => askDelete([m.id])}
                      className="text-gray-500 hover:text-red-400 transition-colors"
                      title={t('hist_delete_confirm')}
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {expanded && <ContentModal text={expanded} onClose={() => setExpanded(null)} />}

      {confirmDelete && (
        <ConfirmModal
          message={confirmDelete.label}
          onConfirm={doDelete}
          onCancel={() => !deleting && setConfirmDelete(null)}
        />
      )}
    </div>
  )
}
