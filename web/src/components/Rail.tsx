import { NavLink } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { useAuthStore } from '../lib/authStore'
import AnimatedNumber from './AnimatedNumber'

const LINKS = [
  { to: '/', label: '生成', icon: '✦', end: true },
  { to: '/assets', label: '资产', icon: '▤' },
  { to: '/characters', label: '角色', icon: '☺' },
  { to: '/presets', label: '预设', icon: '◈' },
]

// Left icon rail (jimeng's structural pattern, §19.0①) replacing the old
// top bar — six tab labels plus balance plus logout couldn't fit one
// horizontal row without crowding out the creation surface itself, and a
// vertical rail is where jimeng puts its own suspended/notification badges,
// which is exactly where ours will eventually land too (once GET /jobs
// exists to back a real 作业 entry — deliberately not added yet, see the
// blueprint's batch-2 note, rather than fabricate a badge with no backing
// data).
export default function Rail() {
  const accessToken = useAuthStore((s) => s.accessToken)
  const logout = useAuthStore((s) => s.logout)
  const me = useQuery({ queryKey: ['me'], queryFn: api.me, enabled: !!accessToken })

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
              `flex w-16 flex-col items-center gap-1 rounded-xl py-2 text-[11px] transition ${
                isActive ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-500 hover:bg-zinc-900 hover:text-zinc-300'
              }`
            }
          >
            <span className="text-base leading-none">{l.icon}</span>
            {l.label}
          </NavLink>
        ))}
      </nav>

      <div className="flex flex-col items-center gap-3 pt-2">
        {accessToken ? (
          <>
            <div className="flex flex-col items-center text-[11px] text-violet-400">
              <span className="text-sm">✦</span>
              <AnimatedNumber value={me.data?.balance ?? 0} className="font-mono" />
            </div>
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
