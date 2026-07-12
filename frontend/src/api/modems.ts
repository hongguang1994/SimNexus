import api from './client'

export interface Modem {
  id: number
  device_path: string | null
  mm_object_path: string | null
  imei: string | null
  manufacturer: string | null
  model: string | null
  phone_number: string | null
  operator: string | null
  signal_quality: number
  status: 'connected' | 'disconnected' | 'error' | 'unknown'
  alias: string | null
  is_active: boolean
  last_seen: string | null
  created_at: string
  access_technologies: string | null
  registration_state: string | null
  tx_bytes: number | null
  rx_bytes: number | null
  connection_duration: number | null
  vowifi_mode: boolean
  vowifi_epdg_ip: string | null
  vowifi_at_port: string | null
  vowifi_airplane: boolean
}

export interface VowifiStep {
  name: string
  state: 'pending' | 'running' | 'ok' | 'fail'
  detail: string
  at: string
}

export interface VowifiSessionInfo {
  epdg_ip: string
  epdg_from_dns: boolean
  tunnel_ipv6: string
  pcscf_count: number
  registered: boolean
}

export interface ModemDetail extends Modem {
  sms_sent: number
  sms_received: number
  sms_today: number
  vowifi_running: boolean
  vowifi_steps: VowifiStep[] | null
  vowifi_info: VowifiSessionInfo | null
}

export const getModemsApi = () => api.get<Modem[]>('/modems/')
export const getAvailableModemsApi = () => api.get<Modem[]>('/modems/available')
export const getModemDetailApi = (id: number) => api.get<ModemDetail>(`/modems/${id}/detail`)
export const updateModemApi = (id: number, data: { alias?: string }) => api.patch<Modem>(`/modems/${id}`, data)
export const refreshModemApi = (id: number) => api.post<Modem>(`/modems/${id}/refresh`)

// 切换该卡的 VoWiFi 模式（仅管理员）。开启后该卡走自建 VoWiFi 协议栈发短信。
export const setVowifiModeApi = (
  id: number,
  data: { vowifi_mode?: boolean; vowifi_epdg_ip?: string; vowifi_at_port?: string },
) => api.patch<Modem>(`/modems/${id}/vowifi`, data)

// 切换该卡的飞行模式（VoWiFi 期间关射频，不在蜂窝基站注册）。仅管理员。
export const setAirplaneApi = (id: number, on: boolean) =>
  api.patch<{ airplane: boolean }>(`/modems/${id}/airplane`, { airplane: on })
