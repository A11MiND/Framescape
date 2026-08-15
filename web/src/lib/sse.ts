import { useAuthStore } from './authStore'

const API_BASE = import.meta.env.VITE_API_BASE ?? 'http://127.0.0.1:8080/api/v1'

interface SSEHandlers {
  onOpen?: () => void
  onEvent?: (event: string, data: unknown) => void
  onError?: () => void
}

// GET /jobs/{bizID}/events sits behind requireAuth's Bearer-only check (see
// internal/interfaces/http/middleware.go) — native EventSource can't set an
// Authorization header, so this hand-rolls the SSE wire format
// ("event: name\ndata: payload\n\n" frames, matching sse.go's
// fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", ...) exactly) over a
// plain authenticated fetch() + ReadableStream instead. This is an
// accelerant, never the only path to fresh data — useJobStream falls back
// to its polling interval on any onError.
export function subscribeJobEvents(bizId: string, handlers: SSEHandlers): () => void {
  const controller = new AbortController()
  const token = useAuthStore.getState().accessToken

  ;(async () => {
    try {
      const resp = await fetch(`${API_BASE}/jobs/${bizId}/events`, {
        headers: {
          Accept: 'text/event-stream',
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        signal: controller.signal,
      })
      if (!resp.ok || !resp.body) throw new Error(`sse http ${resp.status}`)
      handlers.onOpen?.()

      const reader = resp.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        let sep: number
        while ((sep = buf.indexOf('\n\n')) !== -1) {
          const frame = buf.slice(0, sep)
          buf = buf.slice(sep + 2)
          let event = 'message'
          let data = ''
          for (const line of frame.split('\n')) {
            if (line.startsWith('event:')) event = line.slice(6).trim()
            else if (line.startsWith('data:')) data += line.slice(5).trim()
          }
          if (!data) continue // comment frames (": heartbeat") carry no data line
          let parsed: unknown = data
          try {
            parsed = JSON.parse(data)
          } catch {
            // keep raw string — every real frame here is JSON, but don't crash on one that isn't
          }
          handlers.onEvent?.(event, parsed)
          if (event === 'done') {
            controller.abort()
            return
          }
        }
      }
    } catch (err) {
      if ((err as Error).name !== 'AbortError') handlers.onError?.()
    }
  })()

  return () => controller.abort()
}
