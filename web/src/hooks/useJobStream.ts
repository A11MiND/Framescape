import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type JobResponse } from '../lib/api'
import { subscribeJobEvents } from '../lib/sse'

export type StreamState = 'idle' | 'live' | 'reconnecting' | 'polling'

// F7.3/§13.5: SSE is the primary channel, the 1.5s poll from before this
// hook existed is now only a backstop — it stays disabled while SSE is
// confirmed live and re-arms the moment it isn't. §19.5.3's disconnect row
// ("静默降级为轮询，仅在断线超 30 秒后显示细小的提示条，不用 Toast") is why
// this exposes a streamState instead of a plain boolean: 'reconnecting' is
// its own state (silence > 30s while otherwise live), distinct from
// 'polling' (SSE errored outright) and from the brief startup gap ('idle').
export function useJobStream(bizId: string | null | undefined) {
  const queryClient = useQueryClient()
  const [streamState, setStreamState] = useState<StreamState>('idle')
  const lastFrameAt = useRef(0)

  const query = useQuery<JobResponse>({
    queryKey: ['job', bizId],
    queryFn: () => api.getJob(bizId!),
    enabled: !!bizId,
    refetchInterval: (q) => {
      // Found live, alongside an identical bug in AssetDetail.tsx: a bizId
      // that 404s forever (bad link, wrong-user job — jobsvc.Get's own
      // user_id filter) never gets a `.status`, so the old check here
      // never matched and this polled every 1500ms indefinitely. Bail out
      // once react-query's own retries are exhausted and the query has
      // settled into a terminal error, rather than hammering a dead
      // endpoint forever.
      if (q.state.status === 'error') return false
      const status = q.state.data?.status
      if (status === 'succeeded' || status === 'failed') return false
      return streamState === 'live' ? false : 1500
    },
  })

  useEffect(() => {
    if (!bizId) return
    setStreamState('idle')
    lastFrameAt.current = Date.now()

    const unsubscribe = subscribeJobEvents(bizId, {
      onOpen: () => {
        lastFrameAt.current = Date.now()
        setStreamState('live')
      },
      onEvent: () => {
        lastFrameAt.current = Date.now()
        setStreamState('live')
        queryClient.invalidateQueries({ queryKey: ['job', bizId] })
      },
      // Same "still alive" signal as onEvent, minus the query invalidation
      // (a heartbeat carries no job data, there's nothing to refetch) — see
      // sse.ts's own doc on why this exists: without it, any generation
      // quiet for more than 30s (routine for video) falsely read as a
      // flaky connection when it was never actually one.
      onHeartbeat: () => {
        lastFrameAt.current = Date.now()
        setStreamState((cur) => (cur === 'reconnecting' ? 'live' : cur))
      },
      onError: () => setStreamState('polling'),
    })

    // Backend heartbeats every 15s (sse.go) — two missed in a row (30s of
    // silence) means the connection is dead air even though no error fired.
    const watchdog = setInterval(() => {
      const silentFor = Date.now() - lastFrameAt.current
      setStreamState((cur) => (cur === 'live' && silentFor > 30_000 ? 'reconnecting' : cur))
    }, 5000)

    return () => {
      unsubscribe()
      clearInterval(watchdog)
    }
  }, [bizId, queryClient])

  return { ...query, streamState }
}
