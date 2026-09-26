import { useAuthStore } from '../authStore'

// Base /api/v1; errors arrive as {code, message, params?, request_id}.
export const API_BASE: string = import.meta.env.VITE_API_BASE ?? 'http://127.0.0.1:8080/api/v1'

export class ApiError extends Error {
  code: string
  status: number
  /** Values for the localized message (limits, allowed values). */
  params: Record<string, unknown>
  constructor(code: string, message: string, status: number, params: Record<string, unknown> = {}) {
    super(message)
    this.code = code
    this.status = status
    this.params = params
  }
}

interface TokenPair {
  access_token: string
  refresh_token: string
}

let refreshPromise: Promise<boolean> | null = null

/** Refreshes the access token once for all concurrent callers; false signs out. */
export async function ensureFreshToken(): Promise<boolean> {
  const { refreshToken, setTokens, logout } = useAuthStore.getState()
  if (!refreshToken) return false
  if (!refreshPromise) {
    refreshPromise = fetch(`${API_BASE}/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
      .then(async (resp) => {
        if (!resp.ok) throw new Error('refresh failed')
        const data = (await resp.json()) as TokenPair
        setTokens(data.access_token, data.refresh_token)
        return true
      })
      .catch(() => {
        logout()
        return false
      })
      .finally(() => {
        refreshPromise = null
      })
  }
  return refreshPromise
}

export interface RequestOptions {
  auth?: boolean
  idempotencyKey?: string
  signal?: AbortSignal
  _retried?: boolean
}

/**
 * A JSON request. A 401 about the session itself refreshes the token once
 * and replays; other 401s (a wrong current password) are ordinary errors.
 */
export async function request<T>(method: string, path: string, body?: unknown, opts: RequestOptions = {}): Promise<T> {
  const { auth = true, idempotencyKey, signal, _retried = false } = opts
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (auth) {
    const token = useAuthStore.getState().accessToken
    if (token) headers.Authorization = `Bearer ${token}`
  }
  if (idempotencyKey) headers['Idempotency-Key'] = idempotencyKey
  const resp = await fetch(`${API_BASE}${path}`, {
    method,
    headers,
    signal,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (resp.status === 401 && auth && !_retried && path !== '/auth/refresh') {
    const code = await resp
      .clone()
      .json()
      .then((d: { code?: string }) => d.code)
      .catch(() => undefined)
    if ((code === undefined || code === 'unauthorized') && (await ensureFreshToken())) return request<T>(method, path, body, { ...opts, _retried: true })
  }
  if (!resp.ok) {
    const data = await resp.json().catch(() => ({ code: 'unknown', message: resp.statusText }))
    throw new ApiError(data.code ?? 'unknown', data.message ?? resp.statusText, resp.status, data.params ?? {})
  }
  if (resp.status === 204) return undefined as T
  return (await resp.json()) as T
}

/** Builds a query string from defined values. */
export function query(params: Record<string, string | number | boolean | undefined | null>): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') q.set(k, String(v))
  }
  const s = q.toString()
  return s ? `?${s}` : ''
}
