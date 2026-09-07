import { Navigate, Route, Routes } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import Login from './pages/Login'
import Studio from './pages/Studio'
import Characters from './pages/Characters'
import Presets from './pages/Presets'
import Assets from './pages/Assets'
import AssetDetail from './pages/AssetDetail'
import Community from './pages/Community'
import Jobs from './pages/Jobs'
import JobDetail from './pages/JobDetail'
import Credits from './pages/Credits'
import Projects from './pages/Projects'
import Settings from './pages/Settings'
import Admin from './pages/Admin'
import { useAuthStore } from './lib/authStore'
import { api } from './lib/api'

function RequireAuth({ children }: { children: React.ReactNode }) {
  const accessToken = useAuthStore((s) => s.accessToken)
  if (!accessToken) return <Navigate to="/login" replace />
  return <>{children}</>
}

// Route-level gate on top of RequireAuth — a non-admin who somehow lands on
// /admin (bookmark, stale link) gets bounced home rather than seeing a page
// full of 403s. The real access boundary is server-side (requireAdmin,
// every handler in admin.go) regardless of what this does; this is UX, not
// security.
function RequireAdmin({ children }: { children: React.ReactNode }) {
  const me = useQuery({ queryKey: ['me'], queryFn: api.me })
  if (me.isLoading) return null
  if (!me.data?.is_admin) return <Navigate to="/" replace />
  return <>{children}</>
}

// PRD §19.3: `/` is 创作台（首页，登录/匿名皆可进入）— Studio itself
// degrades gracefully for guests (F1.2's trial only, no authed queries)
// rather than sitting behind RequireAuth. `/studio` is kept as a redirect
// for any old bookmarks/links. /jobs and /credits are batch-2's payoff —
// their backing endpoints (GET /jobs, GET /credits/*) didn't exist when
// the rest of this route table was first built.
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
        path="/assets/:assetId"
        element={
          <RequireAuth>
            <AssetDetail />
          </RequireAuth>
        }
      />
      {/* Guest-accessible like `/` — the login page's "browse the community
          first" link (Login.tsx) needs somewhere to send a visitor who
          hasn't signed up yet, and handleCommunityFeed's own read is
          already caller-independent (see server.go's public-route doc).
          Community.tsx itself degrades for guests the same way Studio.tsx
          does: the streak panel and "我发布的" tab, both inherently
          account-scoped, only render once there's an accessToken. */}
      <Route path="/community" element={<Community />} />
      <Route
        path="/jobs"
        element={
          <RequireAuth>
            <Jobs />
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
      <Route
        path="/credits"
        element={
          <RequireAuth>
            <Credits />
          </RequireAuth>
        }
      />
      <Route
        path="/projects"
        element={
          <RequireAuth>
            <Projects />
          </RequireAuth>
        }
      />
      <Route
        path="/settings"
        element={
          <RequireAuth>
            <Settings />
          </RequireAuth>
        }
      />
      <Route
        path="/admin"
        element={
          <RequireAuth>
            <RequireAdmin>
              <Admin />
            </RequireAdmin>
          </RequireAuth>
        }
      />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
