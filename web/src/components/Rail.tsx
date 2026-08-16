import { NavLink } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { useAuthStore } from '../lib/authStore'
import { setStoredLang, type Lang } from '../i18n'
import AnimatedNumber from './AnimatedNumber'
import NotificationCenter from './NotificationCenter'

const LINKS = [
  { to: '/', labelKey: 'rail.generate', icon: '✦', end: true },
  { to: '/jobs', labelKey: 'rail.jobs', icon: '◷' },
  { to: '/assets', labelKey: 'rail.assets', icon: '▤' },
  { to: '/projects', labelKey: 'rail.projects', icon: '▧' },
  { to: '/characters', labelKey: 'rail.characters', icon: '☺' },
  { to: '/presets', labelKey: 'rail.presets', icon: '◈' },
]

// Left icon rail replacing the old top bar on desktop — six tab labels plus
// balance plus logout couldn't fit one horizontal row without crowding out
// the creation surface itself. Below `lg` (1024px) it flips back into a
// horizontal bar (AppShell's own doc covers why the outer flex direction
// has to flip in step) — narrower than the sidebar was ever meant to be, so
// this one scrolls horizontally (`overflow-x-auto`) rather than trying to
// cram all nine items (brand, six nav links, lang toggle, credits, logout)
// into a fixed width. 作业's own badge lands below (batch 2 added GET
// /jobs, finally giving this entry something real to point at instead of a
// fabricated count).
export default function Rail() {
  const { t, i18n } = useTranslation()
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

  const lang = (i18n.language === 'en' ? 'en' : 'zh') as Lang
  const toggleLang = () => setStoredLang(lang === 'zh' ? 'en' : 'zh')

  return (
    <aside className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-zinc-800 bg-zinc-950 px-3 py-2 lg:h-screen lg:w-[76px] lg:flex-col lg:overflow-visible lg:border-b-0 lg:border-r lg:px-0 lg:py-5">
      <div className="mr-1 flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-gradient-to-br from-violet-400 to-violet-600 text-base text-white shadow-lg shadow-violet-950/40 lg:mb-4 lg:mr-0">
        ✦
      </div>

      <nav className="flex items-center gap-1 lg:flex-1 lg:flex-col lg:gap-1.5">
        {LINKS.map((l) => (
          <NavLink
            key={l.to}
            to={l.to}
            end={l.end}
            className={({ isActive }) =>
              `relative flex w-14 shrink-0 flex-col items-center gap-0.5 rounded-xl px-1 py-1.5 text-[10px] transition lg:w-16 lg:gap-1 lg:py-2 lg:text-[11px] ${
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
            {t(l.labelKey)}
          </NavLink>
        ))}
      </nav>

      <div className="ml-1 flex shrink-0 items-center gap-2 lg:ml-0 lg:flex-col lg:gap-3 lg:pt-2">
        <button
          onClick={toggleLang}
          title={t('rail.switchLanguage')}
          className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-zinc-800 text-[10px] font-medium text-zinc-500 transition hover:border-zinc-700 hover:text-zinc-300"
        >
          {lang === 'zh' ? 'EN' : '中'}
        </button>
        {accessToken ? (
          <>
            <NotificationCenter />
            <NavLink to="/credits" className="flex shrink-0 flex-col items-center text-[11px] text-violet-400 hover:text-violet-300">
              <span className="text-sm">✦</span>
              <AnimatedNumber value={me.data?.balance ?? 0} className="font-mono" />
            </NavLink>
            <NavLink
              to="/settings"
              title={t('settings.title')}
              className={({ isActive }) =>
                `flex h-8 w-8 shrink-0 items-center justify-center rounded-full border text-xs transition ${
                  isActive ? 'border-violet-500 text-violet-300' : 'border-zinc-800 text-zinc-500 hover:border-zinc-700 hover:text-zinc-300'
                }`
              }
            >
              ⚙
            </NavLink>
            <button
              onClick={logout}
              title={t('rail.logout')}
              className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-zinc-800 text-xs text-zinc-500 transition hover:border-zinc-700 hover:text-zinc-300"
            >
              ⏻
            </button>
          </>
        ) : (
          <NavLink
            to="/login"
            className="flex w-14 shrink-0 flex-col items-center gap-1 rounded-xl py-1.5 text-[10px] text-zinc-500 transition hover:bg-zinc-900 hover:text-zinc-300 lg:w-16 lg:py-2 lg:text-[11px]"
          >
            <span className="text-base leading-none">◍</span>
            {t('login.signIn')}
          </NavLink>
        )}
      </div>
    </aside>
  )
}
