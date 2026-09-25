import { Suspense } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { Skeleton } from '../../ui'
import { useAuthStore } from '../../lib/authStore'
import { jobsApi } from '../../lib/api/jobs'
import { keys } from '../../lib/api/keys'
import { useMe } from '../useMe'
import { ErrorBoundary } from '../ErrorBoundary'
import { MobileNav } from './MobileNav'
import { Sidebar } from './Sidebar'
import { TopBar } from './TopBar'

export function PageFallback() {
  return (
    <div className="flex flex-col gap-4 p-6" aria-busy="true">
      <Skeleton className="h-8 w-48" />
      <Skeleton className="h-40 w-full" />
      <Skeleton className="h-40 w-full" />
    </div>
  )
}

/** The creator app: sidebar on desktop, bottom navigation below 1024px. */
export function AppLayout() {
  const { t } = useTranslation('shell')
  const signedIn = useAuthStore((s) => Boolean(s.accessToken))
  const me = useMe()
  const counts = useQuery({ queryKey: keys.jobs.summary(), queryFn: () => jobsApi.summary(), enabled: signedIn })
  const needsReview = counts.data?.needs_review ?? null
  const { pathname } = useLocation()
  return (
    <div className="flex h-dvh bg-bg text-fg">
      <a
        href="#main"
        className="sr-only z-50 rounded-card bg-surface px-3 py-2 text-body text-fg focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        {t('skipToContent')}
      </a>
      <ErrorBoundary>
        <Sidebar needsReview={needsReview} />
      </ErrorBoundary>
      <div className="flex min-w-0 flex-1 flex-col">
        <ErrorBoundary>
          <TopBar me={me.data} counts={counts.data} countsFailed={counts.isError} />
        </ErrorBoundary>
        <main id="main" tabIndex={-1} className="min-h-0 flex-1 overflow-y-auto pb-[calc(56px+env(safe-area-inset-bottom))] outline-none lg:pb-0">
          <ErrorBoundary resetKey={pathname}>
            <Suspense fallback={<PageFallback />}>
              <Outlet />
            </Suspense>
          </ErrorBoundary>
        </main>
      </div>
      <MobileNav needsReview={needsReview} />
    </div>
  )
}
