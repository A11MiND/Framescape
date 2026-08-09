import { NavLink } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { useAuthStore } from '../lib/authStore'

const LINKS = [
  { to: '/studio', label: '创作台' },
  { to: '/assets', label: '素材库' },
  { to: '/characters', label: '角色库' },
  { to: '/presets', label: '预设库' },
]

// Shared top bar (PRD §19.3 nav) so /studio, /characters, /presets read as
// one app instead of three disconnected pages.
export default function Nav() {
  const logout = useAuthStore((s) => s.logout)
  const me = useQuery({ queryKey: ['me'], queryFn: api.me })

  return (
    <header className="flex items-center justify-between border-b border-zinc-800 px-6 py-3">
      <nav className="flex items-center gap-1">
        {LINKS.map((l) => (
          <NavLink
            key={l.to}
            to={l.to}
            className={({ isActive }) =>
              `rounded-lg px-3 py-1.5 text-sm transition ${
                isActive ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-400 hover:bg-zinc-900'
              }`
            }
          >
            {l.label}
          </NavLink>
        ))}
      </nav>
      <div className="flex items-center gap-4 text-sm">
        <span className="text-violet-400">✦ {me.data?.balance ?? '…'}</span>
        <button onClick={logout} className="text-zinc-400 hover:text-zinc-200">
          退出登录
        </button>
      </div>
    </header>
  )
}
