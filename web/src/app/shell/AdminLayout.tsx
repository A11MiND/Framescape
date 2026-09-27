import { Suspense } from 'react'
import { Link, Outlet, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ArrowLeft, Receipt, UsersRound } from 'lucide-react'
import { cn } from '../../ui'
import { Brand } from './Brand'
import { PageFallback } from './AppLayout'
import { ErrorBoundary } from '../ErrorBoundary'
import { LanguageSwitch } from './TopBar'

const ITEMS = [
  { to: '/admin/spend', key: 'spend', icon: Receipt },
  { to: '/admin/users', key: 'users', icon: UsersRound },
] as const

/** The admin console has its own navigation, separate from the creator app. */
export function AdminLayout() {
  const { t } = useTranslation('shell')
  const { pathname } = useLocation()
  return (
    <div className="flex h-dvh flex-col bg-bg text-fg lg:flex-row">
      <nav aria-label={t('admin.title')} className="flex shrink-0 flex-col border-b border-border bg-surface lg:w-nav lg:border-r lg:border-b-0">
        <div className="flex h-topbar items-center gap-2 px-5">
          <Brand />
          <span className="text-caption text-fg-muted">{t('admin.title')}</span>
        </div>
        <ul className="flex gap-1 px-3 pb-2 lg:flex-col lg:pt-2">
          {ITEMS.map((it) => {
            const current = pathname === it.to
            return (
              <li key={it.to}>
                <Link
                  to={it.to}
                  aria-current={current ? 'page' : undefined}
                  className={cn(
                    'flex h-10 items-center gap-3 rounded-card px-3 text-body font-medium',
                    current ? 'bg-primary-soft text-primary-text' : 'text-fg-muted hover:bg-surface-2 hover:text-fg',
                  )}
                >
                  <it.icon aria-hidden className="size-5" />
                  {t(`admin.${it.key}`)}
                </Link>
              </li>
            )
          })}
          <li className="lg:mt-2 lg:border-t lg:border-border lg:pt-2">
            <Link to="/" className="flex h-10 items-center gap-3 rounded-card px-3 text-body text-fg-muted hover:bg-surface-2 hover:text-fg">
              <ArrowLeft aria-hidden className="size-5" />
              {t('admin.backToApp')}
            </Link>
          </li>
        </ul>
        <div className="mt-auto px-5 pb-3">
          <LanguageSwitch />
        </div>
      </nav>
      <main id="main" className="min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto">
        <ErrorBoundary resetKey={pathname}>
          <Suspense fallback={<PageFallback />}>
            <Outlet />
          </Suspense>
        </ErrorBoundary>
      </main>
    </div>
  )
}
