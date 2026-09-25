import { lazy } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { AdminLayout } from './shell/AdminLayout'
import { AppLayout } from './shell/AppLayout'
import CreateRoute from './CreateRoute'
import { RedirectKeeping, RequireAdmin, RequireAuth } from './guards'
import { lastCreateMode, prefillTarget } from './routing'

const Login = lazy(() => import('../pages/Login'))
const Characters = lazy(() => import('../pages/Characters'))
const Presets = lazy(() => import('../pages/Presets'))
const Assets = lazy(() => import('../pages/Assets'))
const AssetDetail = lazy(() => import('../pages/AssetDetail'))
const Community = lazy(() => import('../pages/Community'))
const TaskCenter = lazy(() => import('../features/tasks/TaskCenter'))
const JobDetailPage = lazy(() => import('../features/tasks/detail/JobDetailPage'))
const Credits = lazy(() => import('../pages/Credits'))
const Projects = lazy(() => import('../pages/Projects'))
const Settings = lazy(() => import('../pages/Settings'))
const AdminSpend = lazy(() => import('../pages/AdminSpend'))
const AdminUsers = lazy(() => import('../pages/AdminUsers'))

/** `/` opens the mode a prefill belongs to, else the mode used last (image on a first visit). */
function Home() {
  const location = useLocation()
  const target = prefillTarget(location.state)
  return (
    <Navigate
      replace
      to={{ pathname: `/create/${target?.mode ?? lastCreateMode()}`, search: location.search }}
      state={target ? target.state : location.state}
    />
  )
}

function Authed({ children }: { children: React.ReactNode }) {
  return <RequireAuth>{children}</RequireAuth>
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route element={<AppLayout />}>
        <Route path="/" element={<Home />} />
        <Route path="/create/:mode?" element={<CreateRoute />} />
        <Route path="/community" element={<Community />} />
        <Route path="/jobs" element={<Authed><TaskCenter /></Authed>} />
        <Route path="/jobs/:bizId" element={<Authed><JobDetailPage /></Authed>} />
        <Route path="/assets" element={<Authed><Assets /></Authed>} />
        <Route path="/assets/:assetId" element={<Authed><AssetDetail /></Authed>} />
        <Route path="/projects" element={<Authed><Projects /></Authed>} />
        <Route path="/characters" element={<Authed><Characters /></Authed>} />
        <Route path="/presets" element={<Authed><Presets /></Authed>} />
        <Route path="/credits" element={<Authed><Credits /></Authed>} />
        <Route path="/settings" element={<Authed><Settings /></Authed>} />
      </Route>
      <Route
        element={
          <RequireAuth>
            <RequireAdmin>
              <AdminLayout />
            </RequireAdmin>
          </RequireAuth>
        }
      >
        <Route path="/admin/spend" element={<AdminSpend />} />
        <Route path="/admin/users" element={<AdminUsers />} />
      </Route>
      <Route path="/admin" element={<RedirectKeeping to="/admin/spend" />} />
      <Route path="/studio" element={<Home />} />
      <Route path="/comics" element={<RedirectKeeping to="/create/comic" />} />
      <Route path="*" element={<Navigate replace to="/" />} />
    </Routes>
  )
}
