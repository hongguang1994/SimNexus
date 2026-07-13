import api from './client'

export interface TelegramMessage {
  id: number
  chat_id: string
  username: string | null
  direction: 'in' | 'out'
  text: string
  created_at: string
  is_command: boolean
  file_id: string | null
  file_type: string | null
}

export interface TelegramConfig {
  bot_token_set: boolean
  chat_id: string
  polling: boolean
}

export const getTelegramMessagesApi = (skip = 0, limit = 100) =>
  api.get<TelegramMessage[]>('/telegram/messages', { params: { skip, limit } })

export const sendTelegramMessageApi = (text: string, chat_id?: string) =>
  api.post('/telegram/send', { text, chat_id })

export const clearTelegramMessagesApi = () =>
  api.delete('/telegram/messages')

export const sendTelegramFileApi = (file: File, caption?: string) => {
  const form = new FormData()
  form.append('file', file)
  if (caption) form.append('caption', caption)
  return api.post('/telegram/send-file', form)
}

export const getTelegramConfigApi = () =>
  api.get<TelegramConfig>('/telegram/config')

// ── Bot 可视化设置 ──
export interface TelegramSettings {
  has_token: boolean
  bot_username: string
  push_chat_id: string
  allowed_chat_ids: string
  polling: boolean
}
export const getTelegramSettingsApi = () => api.get<TelegramSettings>('/telegram/settings')
// 生成 Telegram 绑定码（任意登录用户）
export const genTelegramBindCodeApi = () => api.post<{ code: string; expires_in: number }>('/telegram/bind-code', {})

// 当前账号名下的 Telegram 绑定
export interface TelegramBind { id: number; chat_id: string; username: string; created_at: string }
export const getMyTelegramBindsApi = () => api.get<TelegramBind[]>('/telegram/binds')
export const deleteMyTelegramBindApi = (id: number) => api.delete(`/telegram/binds/${id}`)
export const saveTelegramSettingsApi = (data: { bot_token?: string; push_chat_id: string; allowed_chat_ids: string }) =>
  api.put<TelegramSettings>('/telegram/settings', data)

// ── 自定义命令 ──
export interface TelegramCommand {
  id: number
  command: string
  type: 'reply' | 'send' | 'webhook'
  reply_text: string
  modem_id: number
  content: string
  to_number: string
  webhook_url: string
  enabled: boolean
  description: string
}
export type TelegramCommandInput = Omit<TelegramCommand, 'id'>
export const getTelegramCommandsApi = () => api.get<TelegramCommand[]>('/telegram/commands')
export const createTelegramCommandApi = (data: TelegramCommandInput) => api.post<TelegramCommand>('/telegram/commands', data)
export const updateTelegramCommandApi = (id: number, data: TelegramCommandInput) => api.patch<TelegramCommand>(`/telegram/commands/${id}`, data)
export const deleteTelegramCommandApi = (id: number) => api.delete(`/telegram/commands/${id}`)
