import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useAuthStore } from '../lib/authStore'
import { accountApi } from '../lib/api/account'
import { keys } from '../lib/api/keys'

export const CREATE_MODES = ['image', 'image-sequence', 'gpt', 'video', 'video-sequence', 'comic', 'comic-classic'] as const
export type CreateMode = (typeof CREATE_MODES)[number]

const LAST_MODE_KEY = 'aigc.create.mode'

export function isCreateMode(s: string | undefined): s is CreateMode {
  return (CREATE_MODES as readonly string[]).includes(s ?? '')
}

export function lastCreateMode(): CreateMode {
  try {
    const v = localStorage.getItem(LAST_MODE_KEY) ?? undefined
    return isCreateMode(v) ? v : 'image'
  } catch {
    return 'image'
  }
}

export function rememberCreateMode(mode: CreateMode) {
  try {
    localStorage.setItem(LAST_MODE_KEY, mode)
  } catch {
    // not remembered for the next visit
  }
}

interface PrefillJob {
  workflowName: string
  spec: { comic_mode?: string; image_provider?: string }
}

/**
 * Where a "create again" prefill opens: the mode of the job's workflow, not
 * the mode used last. The comic editor reads its own prefill shape.
 */
export function prefillTarget(state: unknown): { mode: CreateMode; state: unknown } | null {
  const job = (state as { prefillJob?: PrefillJob } | null)?.prefillJob
  if (!job) return null
  const { workflowName, spec } = job
  if (workflowName === 'image.comic4') {
    return spec?.comic_mode ? { mode: 'comic', state: { prefillComic: spec } } : { mode: 'comic-classic', state }
  }
  if (workflowName === 'image.sequence') return { mode: 'image-sequence', state }
  if (workflowName === 'video.single') return { mode: 'video', state }
  if (workflowName === 'video.sequence') return { mode: 'video-sequence', state }
  return { mode: 'image', state }
}

/** Only same-site paths are followed after sign-in. */
export function safeReturnPath(v: unknown): string | null {
  return typeof v === 'string' && v.startsWith('/') && !v.startsWith('//') && !v.startsWith('/login') ? v : null
}

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
  const me = useQuery({ queryKey: keys.me, queryFn: accountApi.me })
  if (me.isLoading) return null
  if (!me.data?.is_admin) return <Navigate replace to="/" />
  return <>{children}</>
}
