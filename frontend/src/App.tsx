import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import Layout from './components/Layout'
import Dashboard from './pages/Dashboard'
import MessageCenter from './pages/MessageCenter'
import ScheduledTasks from './pages/ScheduledTasks'
import SimDetail from './pages/SimDetail'
import SimCards from './pages/SimCards'
import Users from './pages/Users'
import Roles from './pages/Roles'
import SupportAdmin from './pages/SupportAdmin'
import AdminTasks from './pages/AdminTasks'
import Templates from './pages/Templates'
import SimRequests from './pages/SimRequests'
import MyRequests from './pages/MyRequests'
import ResourceLibrary from './pages/ResourceLibrary'
import TelegramAdmin from './pages/TelegramAdmin'
import Logs from './pages/Logs'
import Login from './pages/Login'
import { useAuthStore } from './store/authStore'

function RequireAuth({ children }: { children: React.ReactNode }) {
  const token = useAuthStore(s => s.token)
  if (!token) return <Navigate to="/login" replace />
  return <>{children}</>
}

function RequireAdmin({ children }: { children: React.ReactNode }) {
  const user = useAuthStore(s => s.user)
  if (user?.role !== 'admin') return <Navigate to="/" replace />
  return <>{children}</>
}

function RequireSupport({ children }: { children: React.ReactNode }) {
  const canSupport = useAuthStore(s => s.canSupport)
  if (!canSupport()) return <Navigate to="/" replace />
  return <>{children}</>
}

function RequireApprove({ children }: { children: React.ReactNode }) {
  const canApprove = useAuthStore(s => s.canApprove)
  if (!canApprove()) return <Navigate to="/" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route element={<RequireAuth><Layout /></RequireAuth>}>
          <Route path="/" element={<Dashboard />} />
          <Route path="/sim-cards" element={<SimCards />} />
          <Route path="/modems/:id" element={<SimDetail />} />
          <Route path="/history" element={<MessageCenter />} />
          <Route path="/tasks" element={<ScheduledTasks />} />
          <Route path="/users" element={<RequireAdmin><Users /></RequireAdmin>} />
          <Route path="/roles" element={<RequireAdmin><Roles /></RequireAdmin>} />
          <Route path="/support" element={<RequireSupport><SupportAdmin /></RequireSupport>} />
          <Route path="/admin/tasks" element={<AdminTasks />} />
          <Route path="/templates" element={<Templates />} />
          <Route path="/my-requests" element={<MyRequests />} />
          <Route path="/resources" element={<ResourceLibrary />} />
          <Route path="/admin/sim-requests" element={<RequireApprove><SimRequests /></RequireApprove>} />
          <Route path="/admin/telegram" element={<RequireAdmin><TelegramAdmin /></RequireAdmin>} />
          <Route path="/admin/logs" element={<RequireAdmin><Logs /></RequireAdmin>} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
