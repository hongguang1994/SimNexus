import axios from 'axios'

const api = axios.create({ baseURL: '/api/v1' })

api.interceptors.request.use((config) => {
  const raw = localStorage.getItem('simnexus-auth')
  if (raw) {
    try {
      const { state } = JSON.parse(raw)
      if (state?.token) config.headers.Authorization = `Bearer ${state.token}`
    } catch {}
  }
  return config
})

api.interceptors.response.use(
  (r) => {
    // 统一响应格式 {code, msg, data} → 将 data 字段提升为 r.data，保持调用方不变
    if (r.data && typeof r.data === 'object' && 'code' in r.data) {
      if (r.data.code !== 0) {
        return Promise.reject(new Error(r.data.msg || '请求失败'))
      }
      r.data = r.data.data
    }
    return r
  },
  (err) => {
    if (err.response?.status === 401) {
      localStorage.removeItem('simnexus-auth')
      window.location.href = '/login'
    }
    return Promise.reject(err)
  }
)

export default api
