import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { Search, Plus, MessageSquare, Phone, Pencil, Trash2, X, ChevronLeft, User } from 'lucide-react'
import clsx from 'clsx'
import { getContactsApi, createContactApi, updateContactApi, deleteContactApi, Contact, ContactInput } from '../api/contacts'
import { useT } from '../i18n'

const AVATAR_COLORS = ['bg-blue-500', 'bg-emerald-500', 'bg-violet-500', 'bg-amber-500', 'bg-rose-500', 'bg-cyan-500', 'bg-indigo-500']
function avatarOf(c: Contact) {
  const s = c.name || c.phone || '?'
  let h = 0
  for (const ch of s) h = (h * 31 + ch.charCodeAt(0)) & 0xffff
  return { label: (c.name || c.phone || '?').trim()[0]?.toUpperCase() || '#', color: AVATAR_COLORS[h % AVATAR_COLORS.length] }
}

// 汉字首字母（拼音）分组：用 zh 排序 anchor 边界法，近似 Apple 通讯录的字母索引。
const PY_ANCHORS: [string, string][] = [
  ['A', '啊'], ['B', '芭'], ['C', '擦'], ['D', '搭'], ['E', '蛾'], ['F', '发'], ['G', '噶'], ['H', '哈'],
  ['J', '击'], ['K', '喀'], ['L', '垃'], ['M', '妈'], ['N', '拿'], ['O', '哦'], ['P', '啪'], ['Q', '期'],
  ['R', '然'], ['S', '撒'], ['T', '塌'], ['W', '挖'], ['X', '昔'], ['Y', '压'], ['Z', '匝'],
]
function initialOf(name: string): string {
  const c = (name || '').trim()[0]
  if (!c) return '#'
  if (/[a-zA-Z]/.test(c)) return c.toUpperCase()
  if (/[0-9]/.test(c)) return '#'
  let letter = '#'
  for (const [L, anchor] of PY_ANCHORS) {
    if (c.localeCompare(anchor, 'zh-Hans-CN') >= 0) letter = L
    else break
  }
  return letter
}

export default function Contacts() {
  const t = useT()
  const navigate = useNavigate()
  const [contacts, setContacts] = useState<Contact[]>([])
  const [loading, setLoading] = useState(true)
  const [query, setQuery] = useState('')
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [isMobile, setIsMobile] = useState(false)
  const [editing, setEditing] = useState<null | { id?: number; form: ContactInput }>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    const mq = window.matchMedia('(max-width: 767px)')
    const on = () => setIsMobile(mq.matches)
    on(); mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])

  const load = async () => {
    try { setContacts((await getContactsApi()).data) } finally { setLoading(false) }
  }
  useEffect(() => { load() }, [])

  const filtered = useMemo(() => {
    let list = [...contacts].sort((a, b) => (a.name || a.phone).localeCompare(b.name || b.phone, 'zh-Hans-CN'))
    if (query.trim()) {
      const q = query.trim().toLowerCase()
      list = list.filter(c => c.name.toLowerCase().includes(q) || c.phone.includes(q) || c.company.toLowerCase().includes(q))
    }
    return list
  }, [contacts, query])

  // 按首字母分组，字母段有序（A–Z 然后 #）
  const groups = useMemo(() => {
    const map = new Map<string, Contact[]>()
    for (const c of filtered) {
      const k = initialOf(c.name || c.phone)
      if (!map.has(k)) map.set(k, [])
      map.get(k)!.push(c)
    }
    return [...map.entries()].sort(([a], [b]) => (a === '#' ? 1 : b === '#' ? -1 : a.localeCompare(b)))
  }, [filtered])

  const current = contacts.find(c => c.id === selectedId) || null

  useEffect(() => {
    if (isMobile) return
    if ((!current || !filtered.some(c => c.id === selectedId)) && filtered.length) setSelectedId(filtered[0].id)
  }, [filtered, isMobile]) // eslint-disable-line

  const openCreate = () => setEditing({ form: { name: '', phone: '', company: '', note: '' } })
  const openEdit = (c: Contact) => setEditing({ id: c.id, form: { name: c.name, phone: c.phone, company: c.company, note: c.note } })

  const save = async () => {
    if (!editing) return
    const f = editing.form
    if (!f.name.trim() && !f.phone.trim()) return
    setSaving(true)
    try {
      if (editing.id) { await updateContactApi(editing.id, f) }
      else { const r = await createContactApi(f); setSelectedId(r.data.id) }
      await load()
      setEditing(null)
    } catch (e: any) {
      alert(e?.response?.data?.msg || t('sms_fail_default'))
    } finally { setSaving(false) }
  }

  const remove = async (c: Contact) => {
    if (!confirm(t('contacts_delete_confirm'))) return
    await deleteContactApi(c.id)
    if (selectedId === c.id) setSelectedId(null)
    await load()
  }

  // 发送信息：跳到消息中心并预填号码开新建
  const sendTo = (c: Contact) => {
    if (!c.phone) return
    navigate('/history', { state: { composeTo: c.phone } })
  }

  const showDetail = !!current

  return (
    <div className="flex h-full rounded-2xl overflow-hidden border border-gray-700/60 bg-gray-900/40">
      {/* 左：字母索引列表 */}
      <aside className={clsx('w-full md:w-80 md:shrink-0 border-r border-gray-700/60 flex-col min-h-0 bg-gray-800/30',
        isMobile && showDetail ? 'hidden' : 'flex')}>
        <div className="p-3 border-b border-gray-700/60 flex items-center gap-2">
          <h1 className="text-base font-bold text-white flex-1">{t('nav_contacts')}</h1>
          <button onClick={openCreate} className="p-1.5 rounded-lg text-blue-400 hover:text-blue-300 hover:bg-blue-500/10" title={t('contacts_add')}>
            <Plus className="w-4 h-4" />
          </button>
        </div>
        <div className="px-3 py-2">
          <div className="flex items-center gap-2 bg-gray-900/60 border border-gray-700 rounded-lg px-2.5 py-1.5">
            <Search className="w-4 h-4 text-gray-500 shrink-0" />
            <input value={query} onChange={e => setQuery(e.target.value)} placeholder={t('contacts_search')}
              className="flex-1 min-w-0 bg-transparent text-sm text-white placeholder-gray-500 focus:outline-none" />
          </div>
        </div>
        <div className="flex-1 overflow-y-auto">
          {filtered.length === 0 ? (
            <div className="p-6 text-center text-gray-500 text-sm">{loading ? '…' : t('contacts_empty')}</div>
          ) : groups.map(([letter, items]) => (
            <div key={letter}>
              <div className="sticky top-0 px-4 py-1 text-[11px] font-semibold text-gray-500 bg-gray-800/80 backdrop-blur">{letter}</div>
              {items.map(c => {
                const a = avatarOf(c)
                const active = c.id === selectedId
                return (
                  <button key={c.id} onClick={() => setSelectedId(c.id)}
                    className={clsx('w-full flex items-center gap-3 px-4 py-2 text-left transition-colors', active ? 'bg-blue-600 text-white' : 'hover:bg-gray-700/30')}>
                    <div className={clsx('w-8 h-8 rounded-full flex items-center justify-center text-white text-xs font-bold shrink-0', a.color)}>{a.label}</div>
                    <div className="min-w-0">
                      <div className={clsx('text-sm truncate', active ? 'text-white font-medium' : 'text-gray-100')}>{c.name || c.phone}</div>
                      {c.company && <div className={clsx('text-xs truncate', active ? 'text-blue-100' : 'text-gray-500')}>{c.company}</div>}
                    </div>
                  </button>
                )
              })}
            </div>
          ))}
        </div>
      </aside>

      {/* 右：联系人详情 */}
      <section className={clsx('flex-1 min-w-0 min-h-0 flex-col', isMobile && !showDetail ? 'hidden' : 'flex')}>
        {current ? (
          <>
            <header className="relative px-4 py-3 flex items-center gap-2">
              <button onClick={() => setSelectedId(null)}
                className="md:hidden p-1.5 rounded-lg text-gray-300 hover:text-white hover:bg-gray-700/50"><ChevronLeft className="w-5 h-5" /></button>
              <div className="flex-1" />
              <button onClick={() => openEdit(current)} className="p-1.5 rounded-lg text-gray-400 hover:text-white hover:bg-gray-700/50" title={t('edit')}><Pencil className="w-4 h-4" /></button>
              <button onClick={() => remove(current)} className="p-1.5 rounded-lg text-gray-400 hover:text-red-400 hover:bg-red-500/10" title={t('delete')}><Trash2 className="w-4 h-4" /></button>
            </header>
            <div className="flex-1 overflow-y-auto px-6 pb-8">
              <div className="flex flex-col items-center gap-3 py-4">
                <div className={clsx('w-24 h-24 rounded-full flex items-center justify-center text-white text-4xl font-bold', avatarOf(current).color)}>{avatarOf(current).label}</div>
                <div className="text-2xl font-bold text-white text-center">{current.name || current.phone}</div>
                {current.company && <div className="text-sm text-gray-400">{current.company}</div>}
                <div className="flex items-center gap-3 mt-1">
                  <button onClick={() => sendTo(current)} disabled={!current.phone}
                    className="flex flex-col items-center gap-1 disabled:opacity-40">
                    <span className="w-11 h-11 rounded-full bg-blue-500/20 text-blue-400 flex items-center justify-center"><MessageSquare className="w-5 h-5" /></span>
                    <span className="text-[11px] text-gray-400">{t('contacts_send')}</span>
                  </button>
                </div>
              </div>

              <div className="max-w-md mx-auto mt-4 space-y-3">
                {current.phone && (
                  <div className="rounded-xl bg-gray-800/50 border border-gray-700/60 px-4 py-3 flex items-center gap-3">
                    <Phone className="w-4 h-4 text-green-400 shrink-0" />
                    <div className="min-w-0 flex-1">
                      <div className="text-[11px] text-gray-500">{t('contacts_phone')}</div>
                      <div className="text-sm text-gray-100 font-mono truncate">{current.phone}</div>
                    </div>
                    <button onClick={() => sendTo(current)} className="p-1.5 rounded-lg text-blue-400 hover:bg-blue-500/10" title={t('contacts_send')}><MessageSquare className="w-4 h-4" /></button>
                  </div>
                )}
                {current.note && (
                  <div className="rounded-xl bg-gray-800/50 border border-gray-700/60 px-4 py-3">
                    <div className="text-[11px] text-gray-500 mb-1">{t('contacts_note')}</div>
                    <div className="text-sm text-gray-200 whitespace-pre-wrap break-words">{current.note}</div>
                  </div>
                )}
              </div>
            </div>
          </>
        ) : (
          <div className="flex-1 flex flex-col items-center justify-center text-gray-500 gap-3">
            <User className="w-10 h-10 opacity-30" />
            <span className="text-sm">{t('contacts_no_select')}</span>
          </div>
        )}
      </section>

      {/* 新建 / 编辑弹窗 */}
      {editing && createPortal(
        <div className="fixed inset-0 z-50 bg-black/60 flex items-center justify-center p-4" onClick={() => setEditing(null)}>
          <div className="w-full max-w-md bg-gray-800 border border-gray-700 rounded-2xl shadow-2xl" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between px-5 pt-4 pb-3 border-b border-gray-700">
              <h2 className="text-base font-semibold text-white">{editing.id ? t('edit') : t('contacts_add')}</h2>
              <button onClick={() => setEditing(null)} className="text-gray-500 hover:text-gray-300"><X className="w-5 h-5" /></button>
            </div>
            <div className="p-5 space-y-3">
              {([
                ['name', t('contacts_name'), '张三'],
                ['phone', t('contacts_phone'), '+8613800138000'],
                ['company', t('contacts_company'), ''],
              ] as const).map(([key, label, ph]) => (
                <label key={key} className="block">
                  <span className="text-xs text-gray-400">{label}</span>
                  <input value={editing.form[key] || ''} onChange={e => setEditing(s => s && ({ ...s, form: { ...s.form, [key]: e.target.value } }))}
                    placeholder={ph} className="mt-1 w-full bg-gray-900 border border-gray-600 rounded-lg px-3 py-2 text-sm text-white placeholder-gray-600 focus:outline-none focus:border-blue-500" />
                </label>
              ))}
              <label className="block">
                <span className="text-xs text-gray-400">{t('contacts_note')}</span>
                <textarea value={editing.form.note || ''} onChange={e => setEditing(s => s && ({ ...s, form: { ...s.form, note: e.target.value } }))}
                  rows={3} className="mt-1 w-full resize-none bg-gray-900 border border-gray-600 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-blue-500" />
              </label>
            </div>
            <div className="flex items-center justify-end gap-2 px-5 pb-4">
              <button onClick={() => setEditing(null)} className="px-4 py-2 rounded-lg text-sm text-gray-300 hover:bg-gray-700">{t('cancel')}</button>
              <button onClick={save} disabled={saving || (!editing.form.name.trim() && !editing.form.phone.trim())}
                className="px-4 py-2 rounded-lg text-sm bg-blue-600 hover:bg-blue-500 text-white disabled:opacity-50">{t('save')}</button>
            </div>
          </div>
        </div>,
        document.body,
      )}
    </div>
  )
}
