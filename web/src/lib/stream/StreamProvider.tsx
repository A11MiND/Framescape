import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useAuthStore } from '../authStore'
import { API_BASE, ensureFreshToken } from '../api/client'
import { applyEvent } from './applyEvent'
import { StreamContext, type StreamState, type StreamStatus } from './context'
import { backoffDelay, parseFrames, toStreamEvent, type StreamEvent } from './parser'

// The server sends a heartbeat every 15s; three missed ones mean the
// connection is gone even if the socket has not noticed.
const STALE_AFTER_MS = 45_000

/** One event stream per tab for the signed-in user. */
export function StreamProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const accessToken = useAuthStore((s) => s.accessToken)
  const signedIn = Boolean(accessToken)
  const [status, setStatusState] = useState<StreamStatus>('idle')
  const statusRef = useRef<StreamStatus>('idle')
  const setStatus = useCallback((s: StreamStatus) => {
    statusRef.current = s
    setStatusState(s)
  }, [])
  const [lastSyncAt, setLastSyncAt] = useState<Date | null>(null)
  const listeners = useRef(new Set<(e: StreamEvent) => void>())
  const lastEventId = useRef<number | null>(null)
  const reconnectNow = useRef<() => void>(() => {})

  useEffect(() => {
    if (!signedIn) {
      setStatus('idle')
      lastEventId.current = null
      return
    }
    let stopped = false
    let controller: AbortController | null = null
    let retryTimer: ReturnType<typeof setTimeout> | undefined
    let staleTimer: ReturnType<typeof setTimeout> | undefined
    let attempt = 0

    const touch = () => {
      setLastSyncAt(new Date())
      clearTimeout(staleTimer)
      staleTimer = setTimeout(() => controller?.abort(), STALE_AFTER_MS)
    }

    const schedule = () => {
      if (stopped) return
      setStatus('offline')
      clearTimeout(retryTimer)
      retryTimer = setTimeout(connect, backoffDelay(attempt++))
    }

    async function connect() {
      if (stopped) return
      clearTimeout(retryTimer)
      controller?.abort()
      const ctrl = new AbortController()
      controller = ctrl
      if (statusRef.current !== 'open') setStatus('connecting')
      const headers: Record<string, string> = { Accept: 'text/event-stream' }
      const token = useAuthStore.getState().accessToken
      if (token) headers.Authorization = `Bearer ${token}`
      if (lastEventId.current !== null) headers['Last-Event-ID'] = String(lastEventId.current)
      try {
        const resp = await fetch(`${API_BASE}/stream`, { headers, signal: ctrl.signal })
        if (resp.status === 401) {
          if (await ensureFreshToken()) {
            connect()
          } else {
            setStatus('idle')
          }
          return
        }
        if (!resp.ok || !resp.body) throw new Error(`stream http ${resp.status}`)
        attempt = 0
        setStatus('open')
        touch()
        const reader = resp.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''
        for (;;) {
          const { value, done } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })
          const { frames, rest } = parseFrames(buffer)
          buffer = rest
          if (frames.length > 0 || value.length > 0) touch()
          for (const frame of frames) {
            const e = toStreamEvent(frame)
            if (!e) continue
            lastEventId.current = e.id
            applyEvent(qc, e)
            listeners.current.forEach((l) => l(e))
          }
        }
        if (!stopped) schedule()
      } catch {
        if (!stopped && controller === ctrl) schedule()
      } finally {
        clearTimeout(staleTimer)
      }
    }

    reconnectNow.current = () => {
      attempt = 0
      connect()
    }
    const onOnline = () => reconnectNow.current()
    const onVisible = () => {
      if (document.visibilityState === 'visible' && statusRef.current === 'offline') reconnectNow.current()
    }
    window.addEventListener('online', onOnline)
    document.addEventListener('visibilitychange', onVisible)
    connect()
    return () => {
      stopped = true
      controller?.abort()
      clearTimeout(retryTimer)
      clearTimeout(staleTimer)
      window.removeEventListener('online', onOnline)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [signedIn, qc, setStatus])

  const subscribe = useCallback((listener: (e: StreamEvent) => void) => {
    listeners.current.add(listener)
    return () => {
      listeners.current.delete(listener)
    }
  }, [])
  const value = useMemo<StreamState>(() => ({ status, lastSyncAt, subscribe }), [status, lastSyncAt, subscribe])
  return <StreamContext.Provider value={value}>{children}</StreamContext.Provider>
}
