import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { X, Plus, Pencil, Trash2, Check, Bot, RefreshCw } from 'lucide-react'
import clsx from 'clsx'
import {
  getTelegramSettingsApi, saveTelegramSettingsApi, TelegramSettings as TSettings,
  getTelegramCommandsApi, createTelegramCommandApi, updateTelegramCommandApi, deleteTelegramCommandApi,
  TelegramCommand, TelegramCommandInput,
  getMyTelegramBindsApi, deleteMyTelegramBindApi, TelegramBind,
} from '../api/telegram'
import { getModemsApi, Modem } from '../api/modems'

const emptyCmd: TelegramCommandInput = {
  command: '', type: 'reply', reply_text: '', modem_id: 0, content: '', to_number: '', webhook_url: '', enabled: true, description: '',
}

export default function TelegramSettings({ onClose, onSaved }: { onClose: () => void; onSaved?: () => void }) {
  const [settings, setSettings] = useState<TSettings | null>(null)
  const [token, setToken] = useState('')
  const [pushChat, setPushChat] = useState('')
  const [allowed, setAllowed] = useState('')
  const [savingCfg, setSavingCfg] = useState(false)
  const [cfgMsg, setCfgMsg] = useState('')

  const [cmds, setCmds] = useState<TelegramCommand[]>([])
  const [modems, setModems] = useState<Modem[]>([])
  const [editing, setEditing] = useState<null | { id?: number; form: TelegramCommandInput }>(null)
  const [savingCmd, setSavingCmd] = useState(false)

  const [binds, setBinds] = useState<TelegramBind[]>([])

  const loadBinds = async () => {
    try { setBinds((await getMyTelegramBindsApi()).data) } catch { /* ignore */ }
  }
  const unbind = async (b: TelegramBind) => {
    if (!confirm('确认解绑该 Telegram？')) return
    await deleteMyTelegramBindApi(b.id)
    loadBinds()
  }

  const loadAll = async () => {
    const [s, c] = await Promise.all([getTelegramSettingsApi(), getTelegramCommandsApi()])
    setSettings(s.data); setPushChat(s.data.push_chat_id); setAllowed(s.data.allowed_chat_ids)
    setCmds(c.data)
    getModemsApi().then(r => setModems(r.data)).catch(() => {})
    loadBinds()
  }
  useEffect(() => { loadAll() }, [])

  const saveCfg = async () => {
    setSavingCfg(true); setCfgMsg('')
    try {
      const r = await saveTelegramSettingsApi({ bot_token: token || undefined, push_chat_id: pushChat, allowed_chat_ids: allowed })
      setSettings(r.data); setToken('')
      setCfgMsg(r.data.has_token ? `✅ 已保存${r.data.bot_username ? ` · @${r.data.bot_username}` : ''}` : '已保存')
      onSaved?.()
    } catch (e: any) {
      setCfgMsg('❌ ' + (e?.response?.data?.msg || '保存失败'))
    } finally { setSavingCfg(false) }
  }

  const saveCmd = async () => {
    if (!editing) return
    const f = editing.form
    if (!f.command.trim()) return
    setSavingCmd(true)
    try {
      if (editing.id) await updateTelegramCommandApi(editing.id, f)
      else await createTelegramCommandApi(f)
      setEditing(null)
      setCmds((await getTelegramCommandsApi()).data)
    } catch (e: any) {
      alert(e?.response?.data?.msg || '保存失败')
    } finally { setSavingCmd(false) }
  }

  const toggleCmd = async (c: TelegramCommand) => {
    await updateTelegramCommandApi(c.id, { ...c, enabled: !c.enabled })
    setCmds((await getTelegramCommandsApi()).data)
  }
  const removeCmd = async (c: TelegramCommand) => {
    if (!confirm(`删除命令 /${c.command}？`)) return
    await deleteTelegramCommandApi(c.id)
    setCmds((await getTelegramCommandsApi()).data)
  }

  const inputCls = 'w-full bg-gray-900 border border-gray-600 rounded-lg px-3 py-2 text-sm text-white placeholder-gray-600 focus:outline-none focus:border-blue-500'
  const labelCls = 'text-xs text-gray-400'

  return createPortal(
    <div className="fixed inset-0 z-50 bg-black/60 flex items-center justify-center p-4" onClick={onClose}>
      <div className="w-full max-w-lg max-h-[88vh] overflow-y-auto bg-gray-800 border border-gray-700 rounded-2xl shadow-2xl" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between px-5 pt-4 pb-3 border-b border-gray-700 sticky top-0 bg-gray-800 z-10">
          <h2 className="text-base font-semibold text-white flex items-center gap-2"><Bot className="w-4 h-4 text-blue-400" /> Telegram 设置</h2>
          <button onClick={onClose} className="text-gray-500 hover:text-gray-300"><X className="w-5 h-5" /></button>
        </div>

        {/* Bot 设置 */}
        <div className="p-5 space-y-3 border-b border-gray-700">
          <div className="text-xs font-semibold text-gray-400 uppercase tracking-wider">Bot 配置</div>
          <label className="block">
            <span className={labelCls}>Bot Token（BotFather 获取）</span>
            <input type="password" value={token} onChange={e => setToken(e.target.value)}
              placeholder={settings?.has_token ? '已配置，留空则不修改' : '123456:ABC-DEF...'} className={`mt-1 ${inputCls}`} />
          </label>
          <label className="block">
            <span className={labelCls}>推送 Chat ID（收到短信推给谁）</span>
            <input value={pushChat} onChange={e => setPushChat(e.target.value)} placeholder="123456789" className={`mt-1 ${inputCls} font-mono`} />
          </label>
          <label className="block">
            <span className={labelCls}>允许发命令的 Chat ID（逗号分隔，含推送对象）</span>
            <input value={allowed} onChange={e => setAllowed(e.target.value)} placeholder="123456789,987654321" className={`mt-1 ${inputCls} font-mono`} />
          </label>
          <div className="flex items-center gap-3">
            <button onClick={saveCfg} disabled={savingCfg} className="px-4 py-2 rounded-lg text-sm bg-blue-600 hover:bg-blue-500 text-white disabled:opacity-50">
              {savingCfg ? '保存中…' : '保存并重启'}
            </button>
            {settings && (
              <span className={clsx('text-xs', settings.polling ? 'text-green-400' : 'text-gray-500')}>
                {settings.polling ? `轮询中${settings.bot_username ? ` · @${settings.bot_username}` : ''}` : '未运行'}
              </span>
            )}
            {cfgMsg && <span className="text-xs text-gray-300">{cfgMsg}</span>}
          </div>
        </div>

        {/* 自定义命令 */}
        <div className="p-5 space-y-2">
          <div className="flex items-center justify-between">
            <div className="text-xs font-semibold text-gray-400 uppercase tracking-wider">自定义命令</div>
            <button onClick={() => setEditing({ form: { ...emptyCmd } })} className="flex items-center gap-1 text-xs text-blue-400 hover:text-blue-300">
              <Plus className="w-3.5 h-3.5" /> 新增
            </button>
          </div>

          {cmds.length === 0 && !editing && <div className="text-sm text-gray-500 py-2">暂无自定义命令</div>}

          {cmds.map(c => (
            <div key={c.id} className="flex items-center gap-2 rounded-lg bg-gray-900/50 border border-gray-700/60 px-3 py-2">
              <button onClick={() => toggleCmd(c)} title={c.enabled ? '已启用' : '已停用'}
                className={clsx('w-2 h-2 rounded-full shrink-0', c.enabled ? 'bg-green-400' : 'bg-gray-600')} />
              <div className="min-w-0 flex-1">
                <div className="text-sm text-white font-mono">/{c.command} <span className="text-[10px] px-1 rounded bg-white/10 text-gray-400">{c.type === 'send' ? '发短信' : c.type === 'webhook' ? 'Webhook' : '回复'}</span></div>
                <div className="text-xs text-gray-500 truncate">{c.description || (c.type === 'send' ? c.content : c.type === 'webhook' ? c.webhook_url : c.reply_text)}</div>
              </div>
              <button onClick={() => setEditing({ id: c.id, form: { command: c.command, type: c.type, reply_text: c.reply_text, modem_id: c.modem_id, content: c.content, to_number: c.to_number, webhook_url: c.webhook_url, enabled: c.enabled, description: c.description } })}
                className="p-1.5 text-gray-400 hover:text-white"><Pencil className="w-3.5 h-3.5" /></button>
              <button onClick={() => removeCmd(c)} className="p-1.5 text-gray-400 hover:text-red-400"><Trash2 className="w-3.5 h-3.5" /></button>
            </div>
          ))}

          {/* 命令编辑器 */}
          {editing && (
            <div className="rounded-lg border border-blue-500/40 bg-blue-500/5 p-3 space-y-2.5 mt-2">
              <div className="flex items-center gap-2">
                <span className="text-gray-400 text-sm">/</span>
                <input value={editing.form.command} onChange={e => setEditing(s => s && ({ ...s, form: { ...s.form, command: e.target.value.replace(/[^a-zA-Z0-9_]/g, '') } }))}
                  placeholder="命令名，如 report" className={`${inputCls} flex-1`} />
              </div>
              <div className="flex gap-2">
                {([['reply', '自动回复'], ['send', '发短信'], ['webhook', 'Webhook']] as const).map(([tp, label]) => (
                  <button key={tp} onClick={() => setEditing(s => s && ({ ...s, form: { ...s.form, type: tp } }))}
                    className={clsx('flex-1 py-1.5 rounded-lg text-sm border', editing.form.type === tp ? 'border-blue-500 text-blue-300 bg-blue-500/10' : 'border-gray-600 text-gray-400')}>
                    {label}
                  </button>
                ))}
              </div>

              {editing.form.type === 'reply' ? (
                <label className="block">
                  <span className={labelCls}>回复内容</span>
                  <textarea value={editing.form.reply_text} onChange={e => setEditing(s => s && ({ ...s, form: { ...s.form, reply_text: e.target.value } }))}
                    rows={2} className={`mt-1 ${inputCls} resize-none`} />
                </label>
              ) : editing.form.type === 'webhook' ? (
                <label className="block">
                  <span className={labelCls}>Webhook URL（收到命令时 POST，返回体或 JSON 的 text 作为回复）</span>
                  <input value={editing.form.webhook_url} onChange={e => setEditing(s => s && ({ ...s, form: { ...s.form, webhook_url: e.target.value } }))}
                    placeholder="https://你的服务/telegram-cmd" className={`mt-1 ${inputCls} font-mono`} />
                  <span className="block mt-1 text-[11px] text-gray-500">POST body：{'{'} command, args[], text, chat_id {'}'}</span>
                </label>
              ) : (
                <>
                  <label className="block">
                    <span className={labelCls}>发送设备</span>
                    <select value={editing.form.modem_id} onChange={e => setEditing(s => s && ({ ...s, form: { ...s.form, modem_id: Number(e.target.value) } }))}
                      className={`mt-1 ${inputCls}`}>
                      <option value={0}>自动（单卡在线时）</option>
                      {modems.map(m => <option key={m.id} value={m.id}>#{m.id} {m.alias || m.operator || `SIM ${m.id}`}{m.phone_number ? ` (${m.phone_number})` : ''}</option>)}
                    </select>
                  </label>
                  <label className="block">
                    <span className={labelCls}>短信内容</span>
                    <textarea value={editing.form.content} onChange={e => setEditing(s => s && ({ ...s, form: { ...s.form, content: e.target.value } }))}
                      rows={2} className={`mt-1 ${inputCls} resize-none`} />
                  </label>
                  <label className="block">
                    <span className={labelCls}>预设号码（留空则用命令参数，如 /{editing.form.command || 'cmd'} +44...）</span>
                    <input value={editing.form.to_number} onChange={e => setEditing(s => s && ({ ...s, form: { ...s.form, to_number: e.target.value } }))}
                      placeholder="+8613800138000" className={`mt-1 ${inputCls} font-mono`} />
                  </label>
                </>
              )}
              <label className="block">
                <span className={labelCls}>说明（可选，显示在 /help）</span>
                <input value={editing.form.description} onChange={e => setEditing(s => s && ({ ...s, form: { ...s.form, description: e.target.value } }))}
                  className={`mt-1 ${inputCls}`} />
              </label>
              <div className="flex items-center justify-end gap-2">
                <button onClick={() => setEditing(null)} className="px-3 py-1.5 rounded-lg text-sm text-gray-300 hover:bg-gray-700">取消</button>
                <button onClick={saveCmd} disabled={savingCmd || !editing.form.command.trim()}
                  className="px-3 py-1.5 rounded-lg text-sm bg-blue-600 hover:bg-blue-500 text-white disabled:opacity-50 flex items-center gap-1"><Check className="w-3.5 h-3.5" /> 保存</button>
              </div>
            </div>
          )}
        </div>

        {/* 我的 Telegram 绑定 */}
        <div className="p-5 space-y-2 border-t border-gray-700">
          <div className="flex items-center justify-between">
            <div className="text-xs font-semibold text-gray-400 uppercase tracking-wider">我的 Telegram 绑定</div>
            <button onClick={loadBinds} className="text-gray-500 hover:text-gray-300" title="刷新"><RefreshCw className="w-3.5 h-3.5" /></button>
          </div>
          {binds.length === 0 ? (
            <div className="text-sm text-gray-500 py-1">暂无绑定</div>
          ) : (
            <div className="space-y-1.5">
              {binds.map(b => (
                <div key={b.id} className="flex items-center gap-2 rounded-lg bg-gray-900/50 border border-gray-700/60 px-3 py-2">
                  <div className="min-w-0 flex-1">
                    <div className="text-sm text-white truncate">{b.username ? `@${b.username}` : `Chat ${b.chat_id}`}</div>
                    <div className="text-[11px] text-gray-500 font-mono">{b.chat_id} · {b.created_at}</div>
                  </div>
                  <button onClick={() => unbind(b)} className="p-1.5 text-gray-400 hover:text-red-400" title="解绑"><Trash2 className="w-4 h-4" /></button>
                </div>
              ))}
            </div>
          )}
          <p className="text-[11px] text-gray-500 pt-1">在账户菜单「绑定 Telegram」处生成绑定码来新增绑定。</p>
        </div>
      </div>
    </div>,
    document.body,
  )
}
