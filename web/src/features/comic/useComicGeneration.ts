import { useEffect, useRef } from 'react'
import { createApi, type CreateRequest } from '../../lib/api/create'
import { jobsApi } from '../../lib/api/jobs'
import { ComicError } from '../../lib/comicError'
import { applyGeneratedImage } from '../../lib/comicDocument'
import type { ComicDraft } from './useComicDraft'

export interface ComicGenerationEvents {
  onDone: (usageUnknown: boolean) => void
  onFailed: (errorCode: string | undefined) => void
  onError: (err: unknown) => void
}

/**
 * Submits a comic generation and follows it. The result is applied to the
 * document as it is when the job finishes, so edits made meanwhile are kept.
 */
export function useComicGeneration(draft: ComicDraft, storageKey: string | null, events: ComicGenerationEvents) {
  const latest = useRef(events)
  latest.current = events
  const attempt = useRef<{ payload: string; id: string } | null>(null)
  const pending = draft.doc.pending

  useEffect(() => {
    if (!pending) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      try {
        const job = await jobsApi.get(pending.job_id)
        if (cancelled) return
        if (job.status === 'succeeded') {
          const compose = job.nodes.find((n) => n.name === 'compose')
          const asset = (compose?.outputs as Record<string, unknown> | null)?.['asset-id']
          if (typeof asset !== 'string' || !asset) throw new ComicError('noImage')
          draft.change(applyGeneratedImage(draft.live.current, asset, pending.panel))
          latest.current.onDone((compose?.outputs as Record<string, unknown> | null)?.['usage-known'] === false)
          return
        }
        if (['failed', 'partial', 'cancelled'].includes(job.status)) {
          draft.change({ ...draft.live.current, pending: undefined }, false)
          latest.current.onFailed(job.error_code || job.nodes.find((n) => n.status === 'failed')?.error_code)
          return
        }
      } catch (err) {
        if (!cancelled) latest.current.onError(err)
      }
      if (!cancelled) timer = setTimeout(poll, 2500)
    }
    void poll()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pending?.job_id])

  const quote = (request: CreateRequest) => createApi.estimate(request)

  /** Creates the job at the confirmed price; a retried submit of the same request reuses its key. */
  const submit = async (request: CreateRequest, quoteTotal: number, panel: number) => {
    await draft.persist()
    const payload = JSON.stringify(request)
    const recoverKey = storageKey ? `${storageKey}.submission` : null
    if (recoverKey && !attempt.current) {
      try {
        const raw = localStorage.getItem(recoverKey)
        const saved = raw ? (JSON.parse(raw) as { payload?: string; id?: string }) : null
        if (saved?.payload === payload && saved.id) attempt.current = { payload, id: saved.id }
      } catch {
        // a fresh attempt is used when no valid one was kept
      }
    }
    if (attempt.current?.payload !== payload) attempt.current = { payload, id: crypto.randomUUID() }
    if (recoverKey) localStorage.setItem(recoverKey, JSON.stringify(attempt.current))
    const job = await createApi.create({ ...request, quote_total: quoteTotal }, attempt.current.id)
    attempt.current = null
    if (recoverKey) localStorage.removeItem(recoverKey)
    const next = { ...draft.live.current, pending: { job_id: job.biz_id, panel } }
    draft.change(next, false)
    await draft.persist(next)
  }

  return { quote, submit }
}
