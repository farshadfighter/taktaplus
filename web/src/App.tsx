import { Spin } from 'antd'
import { Navigate, Route, Routes } from 'react-router'
import { AuthProvider, useAuth } from './auth'
import AppLayout from './AppLayout'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import Devices from './pages/Devices'
import DeviceForm from './pages/DeviceForm'
import DeviceDetail from './pages/DeviceDetail'
import Alerts from './pages/Alerts'
import Backups from './pages/Backups'
import BackupView from './pages/BackupView'
import License from './pages/License'
import Users from './pages/Users'
import Audit from './pages/Audit'
import Packages from './pages/Packages'
import Changes from './pages/Changes'
import ChangeDetail from './pages/ChangeDetail'
import Deploy from './pages/Deploy'
import Firmware from './pages/Firmware'
import Scripts from './pages/Scripts'
import ScriptEdit from './pages/ScriptEdit'
import Maintenance from './pages/Maintenance'
import OtpUsers from './pages/OtpUsers'
import OtpSettings from './pages/OtpSettings'
import Analytics from './pages/Analytics'
import LogView from './pages/LogView'
import Reports from './pages/Reports'
import LogSettings from './pages/LogSettings'

function Protected() {
  const { user, loading } = useAuth()
  if (loading) return <Spin fullscreen />
  if (!user) return <Navigate to="/login" replace />
  return <AppLayout />
}

export default function App() {
  return (
    <AuthProvider>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route element={<Protected />}>
          <Route index element={<Dashboard />} />
          <Route path="devices" element={<Devices />} />
          <Route path="devices/new" element={<DeviceForm />} />
          <Route path="devices/:id" element={<DeviceDetail />} />
          <Route path="devices/:id/edit" element={<DeviceForm />} />
          <Route path="alerts" element={<Alerts />} />
          <Route path="backups" element={<Backups />} />
          <Route path="backups/:id" element={<BackupView />} />
          <Route path="license" element={<License />} />
          <Route path="users" element={<Users />} />
          <Route path="audit" element={<Audit />} />
          <Route path="packages" element={<Packages />} />
          <Route path="changes" element={<Changes />} />
          <Route path="changes/:id" element={<ChangeDetail />} />
          <Route path="deploy" element={<Deploy />} />
          <Route path="firmware" element={<Firmware />} />
          <Route path="scripts" element={<Scripts />} />
          <Route path="scripts/:id" element={<ScriptEdit />} />
          <Route path="maintenance" element={<Maintenance />} />
          <Route path="otp/users" element={<OtpUsers />} />
          <Route path="otp/settings" element={<OtpSettings />} />
          <Route path="analytics" element={<Analytics />} />
          <Route path="logs" element={<LogView />} />
          <Route path="logs/settings" element={<LogSettings />} />
          <Route path="reports" element={<Reports />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </AuthProvider>
  )
}
