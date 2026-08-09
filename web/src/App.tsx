import { Navigate, Route, Routes } from 'react-router-dom'
import Login from './pages/Login'
import Studio from './pages/Studio'
import Characters from './pages/Characters'
import Presets from './pages/Presets'
import Assets from './pages/Assets'
import JobDetail from './pages/JobDetail'
import { useAuthStore } from './lib/authStore'

function RequireAuth({ children }: { children: React.ReactNode }) {
  const accessToken = useAuthStore((s) => s.accessToken)
  if (!accessToken) return <Navigate to="/login" replace />
  return <>{children}</>
}

// PRD §19.3 information architecture: /login, /studio, /characters, /presets,
// /assets, /jobs/:bizId (F7.2's DAG view) exist so far. A standalone /jobs
// list page and /credits land as their remaining backing endpoints do.
export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        path="/studio"
        element={
          <RequireAuth>
            <Studio />
          </RequireAuth>
        }
      />
      <Route
        path="/characters"
        element={
          <RequireAuth>
            <Characters />
          </RequireAuth>
        }
      />
      <Route
        path="/presets"
        element={
          <RequireAuth>
            <Presets />
          </RequireAuth>
        }
      />
      <Route
        path="/assets"
        element={
          <RequireAuth>
            <Assets />
          </RequireAuth>
        }
      />
      <Route
        path="/jobs/:bizId"
        element={
          <RequireAuth>
            <JobDetail />
          </RequireAuth>
        }
      />
      <Route path="*" element={<Navigate to="/studio" replace />} />
    </Routes>
  )
}
