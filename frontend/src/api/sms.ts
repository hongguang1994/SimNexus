import api from './client'

export interface SmsMessage {
  id: number
  modem_id: number
  direction: 'inbound' | 'outbound'
  channel: 'cellular' | 'vowifi'
  phone_number: string
  content: string
  status: 'pending' | 'sent' | 'failed' | 'received'
  error_message: string | null
  sent_at: string | null
  received_at: string | null
  created_at: string
}

export interface SmsTemplate {
  id: number
  name: string
  content: string
  variables: string[] | null
  created_at: string
}

export interface ScheduledTask {
  id: number
  name: string
  modem_id: number
  recipients: string[]
  content: string
  cron_expression: string | null
  send_once_at: string | null
  status: 'active' | 'paused' | 'completed' | 'failed'
  last_run_at: string | null
  next_run_at: string | null
  run_count: number
  created_at: string
  created_by_id: number | null
  created_by_username: string | null
}

export interface TaskStats {
  total: number
  active: number
  paused: number
  completed: number
  failed: number
}

export const sendSmsApi = (data: { modem_id: number; phone_number: string; content: string }) =>
  api.post<SmsMessage>('/sms/send', data)

export const getMessagesApi = (params?: { modem_id?: number; direction?: string; skip?: number; limit?: number }) =>
  api.get<SmsMessage[]>('/sms/messages', { params })

export const deleteMessageApi = (id: number) => api.delete(`/sms/messages/${id}`)
export const batchDeleteMessagesApi = (ids: number[]) =>
  api.post<{ deleted: number }>('/sms/messages/batch-delete', { ids })

export const getTemplatesApi = () => api.get<SmsTemplate[]>('/sms/templates')
export const createTemplateApi = (data: { name: string; content: string; variables?: string[] }) =>
  api.post<SmsTemplate>('/sms/templates', data)
export const deleteTemplateApi = (id: number) => api.delete(`/sms/templates/${id}`)

export const getTasksApi = () => api.get<ScheduledTask[]>('/sms/tasks')
export const createTaskApi = (data: Partial<ScheduledTask>) => api.post<ScheduledTask>('/sms/tasks', data)
export const updateTaskApi = (id: number, data: Partial<ScheduledTask>) => api.patch<ScheduledTask>(`/sms/tasks/${id}`, data)
export const deleteTaskApi = (id: number) => api.delete(`/sms/tasks/${id}`)
export const runTaskNowApi = (id: number) => api.post(`/sms/tasks/${id}/run-now`)

// Admin-only
export const adminGetTasksApi = (params?: { status?: string; user_id?: number }) =>
  api.get<ScheduledTask[]>('/sms/admin/tasks', { params })
export const adminGetTaskStatsApi = () => api.get<TaskStats>('/sms/admin/tasks/stats')
export const adminGetTaskHistoryApi = (taskId: number) =>
  api.get<SmsMessage[]>(`/sms/admin/tasks/${taskId}/history`)
