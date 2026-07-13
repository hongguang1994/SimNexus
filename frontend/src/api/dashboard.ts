import client from './client'

export interface SmsTrendDay {
  date: string
  sent: number
  failed: number
  received: number
}

export interface DashboardStats {
  sms_trend: SmsTrendDay[]
  month_sms: { sent: number; failed: number; pending: number; received: number }
  tasks: { active: number; paused: number; completed: number; failed: number }
}

export const getDashboardStatsApi = () => client.get<DashboardStats>('/dashboard/stats')
