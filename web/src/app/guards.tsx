import { useTranslation } from 'react-i18next'
import { ErrorState, Skeleton } from '../ui'
import { errorText } from '../lib/errorText'
import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useAuthStore } from '../lib/authStore'
import { accountApi } from '../lib/api/account'
import { keys } from '../lib/api/keys'

/** Redirects keeping the query, hash and navigation state (prefills, return address). */
export function RedirectKeeping({ to }: { to: string }) {
  const location = useLocation()
  return <Navigate replace to={{ pathname: to, search: location.search, hash: location.hash }} state={location.state} />
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const accessToken = useAuthStore((s) => s.accessToken)
  const location = useLocation()
  if (!accessToken) {
    return <Navigate replace to="/login" state={{ from: location.pathname + location.search + location.hash }} />
  }
  return <>{children}</>
}

/** UX gate only; every admin endpoint is authorized by the server. */
export function RequireAdmin({ children }: { children: ReactNode }) {
  const { t } = useTranslation('ui')
  const me = useQuery({ queryKey: keys.me, queryFn: accountApi.me })
  if (me.isLoading) return <div role="status" className="p-6"><span className="sr-only">{t('state.loading')}</span><Skeleton className="h-48" /></div>
  if (me.isError) return <ErrorState message={errorText(t, me.error)} onRetry={() => me.refetch()} />
  if (!me.data?.is_admin) return <Navigate replace to="/" />
  return <>{children}</>
}
