import { NavLink } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { useAuthStore } from '../lib/authStore'
import AnimatedNumber from './AnimatedNumber'

const LINKS = [
  { to: '/', label: '生成', icon: '✦', end: true },
  { to: '/jobs', label: '作业', icon: '◷' },
  { to: '/assets', label: '资产', icon: '▤' },
  { to: '/projects', label: '项目', icon: '▧' },
  { to: '/characters', label: '角色', icon: '☺' },
  { to: '/presets', label: '预设', icon: '◈' },
]

// Left icon rail (jimeng's structural pattern, §19.0①) replacing the old
// top bar — six tab labels plus balance plus logout couldn't fit one
// horizontal row without crowding out the creation surface itself, and a
// vertical rail is where jimeng puts its own suspended/notification badges,
// which is where 作业's own badge lands below (batch 2 added GET /jobs,
// finally giving this entry something real to point at instead of a
// fabricated count).
export default function Rail() {
  const accessToken = useAuthStore((s) => s.accessToken)
  const logout = useAuthStore((s) => s.logout)
  const me = useQuery({ queryKey: ['me'], queryFn: api.me, enabled: !!accessToken })
  // "running" covers both truly-in-progress jobs and video.sequence's
  // preview gate (job.status never gets its own "suspended" value — see
  // useJobStream/jobResult.ts's own doc on that), so this badge doubles as
  // R12's "有作业等待你决策" reminder without a second query. Capped at 9
  // items fetched — an exact count isn't worth a dedicated endpoint, "9+"
  // reads the same as "37" to a user deciding whether to go look.
  const runningJobs = useQuery({
    queryKey: ['jobs', 'running-badge'],
    queryFn: () => api.listJobs({ status: 'running', limit: 9 }),
    enabled: !!accessToken,
    refetchInterval: 15_000,
  })
  const runningCount = runningJobs.data?.jobs.length ?? 0
  const runningBadge = runningJobs.data?.next_cursor ? '9+' : runningCount > 0 ? String(runningCount) : null

  return (
    <aside className="flex w-[76px] shrink-0 flex-col items-center gap-1 border-r border-zinc-800 bg-zinc-950 py-5">
      <div className="mb-4 flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-to-br from-violet-400 to-violet-600 text-base text-white shadow-lg shadow-violet-950/40">
        ✦
      </div>

      <nav className="flex flex-1 flex-col items-center gap-1.5">
        {LINKS.map((l) => (
          <NavLink
            key={l.to}
            to={l.to}
            end={l.end}
            className={({ isActive }) =>
              `relative flex w-16 flex-col items-center gap-1 rounded-xl py-2 text-[11px] transition ${
                isActive ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-500 hover:bg-zinc-900 hover:text-zinc-300'
              }`
            }
          >
            <span className="relative text-base leading-none">
              {l.icon}
              {l.to === '/jobs' && runningBadge && (
                <span className="absolute -right-2.5 -top-1.5 rounded-full bg-amber-500 px-1 text-[9px] font-medium leading-[14px] text-black">
                  {runningBadge}
                </span>
              )}
            </span>
            {l.label}
          </NavLink>
        ))}
      </nav>

      <div className="flex flex-col items-center gap-3 pt-2">
        {accessToken ? (
          <>
            <NavLink to="/credits" className="flex flex-col items-center text-[11px] text-violet-400 hover:text-violet-300">
              <span className="text-sm">✦</span>
              <AnimatedNumber value={me.data?.balance ?? 0} className="font-mono" />
            </NavLink>
            <button
              onClick={logout}
              title="退出登录"
              className="flex h-8 w-8 items-center justify-center rounded-full border border-zinc-800 text-xs text-zinc-500 transition hover:border-zinc-700 hover:text-zinc-300"
            >
              ⏻
            </button>
          </>
        ) : (
          <NavLink
            to="/login"
            className="flex w-16 flex-col items-center gap-1 rounded-xl py-2 text-[11px] text-zinc-500 transition hover:bg-zinc-900 hover:text-zinc-300"
          >
            <span className="text-base leading-none">◍</span>
            登录
          </NavLink>
        )}
      </div>
    </aside>
  )
}
