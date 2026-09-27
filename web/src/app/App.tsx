import { lazy } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { AdminLayout } from './shell/AdminLayout'
import { AppLayout } from './shell/AppLayout'
import CreateRoute from './CreateRoute'
import { RedirectKeeping, RequireAdmin, RequireAuth } from './guards'
import { lastCreateMode, prefillTarget } from './routing'

const Login = lazy(() => import('../features/auth/LoginPage'))
const Characters = lazy(() => import('../features/characters/CharactersPage'))
const Presets = lazy(() => import('../features/presets/PresetsPage'))
const Assets = lazy(() => import('../features/library/LibraryPage'))
const AssetDetail = lazy(() => import('../features/library/AssetDetailPage'))
const Community = lazy(() => import('../features/community/CommunityPage'))
const TaskCenter = lazy(() => import('../features/tasks/TaskCenter'))
const ComicWorkspace = lazy(() => import('../features/comic/ComicWorkspace'))
const JobDetailPage = lazy(() => import('../features/tasks/detail/JobDetailPage'))
const Credits = lazy(() => import('../features/credits/CreditsPage'))
const Projects = lazy(() => import('../features/projects/ProjectsPage'))
const ProjectDetail = lazy(() => import('../features/projects/ProjectDetailPage'))
const Settings = lazy(() => import('../features/settings/SettingsPage'))
const AdminSpend = lazy(() => import('../features/admin/SpendPage'))
const AdminUsers = lazy(() => import('../features/admin/UsersPage'))

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
        <Route path="/create/comic/:draftId/edit" element={<Authed><ComicWorkspace /></Authed>} />
        <Route path="/community" element={<Community />} />
        <Route path="/jobs" element={<Authed><TaskCenter /></Authed>} />
        <Route path="/jobs/:bizId" element={<Authed><JobDetailPage /></Authed>} />
        <Route path="/assets" element={<Authed><Assets /></Authed>} />
        <Route path="/assets/:assetId" element={<Authed><AssetDetail /></Authed>} />
        <Route path="/projects" element={<Authed><Projects /></Authed>} />
        <Route path="/projects/:projectId" element={<Authed><ProjectDetail /></Authed>} />
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
