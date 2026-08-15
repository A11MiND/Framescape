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

// PRD §19.3: `/` is 创作台（首页，登录/匿名皆可进入）— Studio itself
// degrades gracefully for guests (F1.2's trial only, no authed queries)
// rather than sitting behind RequireAuth. `/studio` is kept as a redirect
// for any old bookmarks/links. A standalone /jobs list page and /credits
// land as their remaining backing endpoints do (no GET /jobs, no
// GET /credits/* yet — see the blueprint's batch-2 backend list).
export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/" element={<Studio />} />
      <Route path="/studio" element={<Navigate to="/" replace />} />
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
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
