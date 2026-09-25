import { useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'
import { Ellipsis, LogOut } from 'lucide-react'
import { Drawer, cn } from '../../ui'
import { useAuthStore } from '../../lib/authStore'
import { FOOTER_NAV, MAIN_NAV, isCurrent, type NavItem } from './nav'
import { LanguageSwitch } from './TopBar'

const PRIMARY: NavItem['key'][] = ['create', 'tasks', 'library']

/** Bottom navigation below 1024px: three destinations plus a sheet with the rest. */
export function MobileNav({ needsReview }: { needsReview: number | null }) {
  const { t } = useTranslation('shell')
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const signedIn = useAuthStore((s) => Boolean(s.accessToken))
  const logout = useAuthStore((s) => s.logout)
  const [more, setMore] = useState(false)
  const all = [...MAIN_NAV, ...FOOTER_NAV].filter((i) => signedIn || !i.auth)
  const primary = all.filter((i) => PRIMARY.includes(i.key))
  const rest = all.filter((i) => !PRIMARY.includes(i.key))
  const restCurrent = rest.some((i) => isCurrent(i, pathname))

  const tab = (label: string, icon: React.ReactNode, current: boolean, badge = 0) => (
    <span className={cn('relative flex h-full flex-col items-center justify-center gap-0.5 text-badge font-medium', current ? 'text-primary-text' : 'text-fg-muted')}>
      {icon}
      {label}
      {badge > 0 && (
        <span className="absolute top-1.5 left-1/2 ml-2 rounded-full bg-warning-soft px-1.5 text-badge font-semibold text-warning-fg tabular-nums">{badge}</span>
      )}
    </span>
  )

  return (
    <>
      <nav
        aria-label={t('nav.label')}
        className="fixed inset-x-0 bottom-0 z-40 border-t border-border bg-surface pb-[env(safe-area-inset-bottom)] lg:hidden"
      >
        <ul className="grid h-14 grid-cols-4">
          {primary.map((item) => {
            const current = isCurrent(item, pathname)
            return (
              <li key={item.key}>
                <Link to={item.to()} aria-current={current ? 'page' : undefined} className="block h-full">
                  {tab(t(`nav.${item.key}`), <item.icon aria-hidden className="size-5" />, current, item.key === 'tasks' ? (needsReview ?? 0) : 0)}
                </Link>
              </li>
            )
          })}
          <li>
            <button type="button" onClick={() => setMore(true)} aria-haspopup="dialog" className="block h-full w-full">
              {tab(t('nav.more'), <Ellipsis aria-hidden className="size-5" />, restCurrent)}
            </button>
          </li>
        </ul>
      </nav>
      <Drawer open={more} onOpenChange={setMore} title={t('more.title')}>
        <ul className="flex flex-col gap-1">
          {rest.map((item) => (
            <li key={item.key}>
              <Link
                to={item.to()}
                onClick={() => setMore(false)}
                aria-current={isCurrent(item, pathname) ? 'page' : undefined}
                className="flex h-12 items-center gap-3 rounded-card px-3 text-body-lg text-fg hover:bg-surface-2 aria-[current=page]:bg-primary-soft aria-[current=page]:text-primary-text"
              >
                <item.icon aria-hidden className="size-5 text-fg-muted" />
                {t(`nav.${item.key}`)}
              </Link>
            </li>
          ))}
        </ul>
        <div className="mt-4 flex items-center justify-between border-t border-border pt-4">
          <span className="text-body text-fg-muted">{t('more.language')}</span>
          <LanguageSwitch />
        </div>
        {signedIn && (
          <button
            type="button"
            onClick={() => {
              setMore(false)
              logout()
              qc.clear()
              navigate('/login')
            }}
            className="mt-4 flex h-12 w-full items-center gap-3 rounded-card px-3 text-body-lg text-danger-fg hover:bg-danger-soft"
          >
            <LogOut aria-hidden className="size-5" />
            {t('top.signOut')}
          </button>
        )}
      </Drawer>
    </>
  )
}
