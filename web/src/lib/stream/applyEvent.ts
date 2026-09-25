import type { QueryClient } from '@tanstack/react-query'
import { keys } from '../api/keys'
import type { StreamEvent } from './parser'

const JOB_LIST_EVENTS = new Set(['job.created', 'job.status', 'job.needs_review', 'job.finished'])
const CREDIT_EVENTS = new Set(['job.created', 'job.finished', 'credits.changed'])

/** Marks the cached data an event changes as stale. */
export function applyEvent(qc: QueryClient, e: StreamEvent) {
  if (e.job_id) qc.invalidateQueries({ queryKey: keys.jobs.detail(e.job_id) })
  if (JOB_LIST_EVENTS.has(e.type)) qc.invalidateQueries({ queryKey: keys.jobs.all })
  if (CREDIT_EVENTS.has(e.type)) qc.invalidateQueries({ queryKey: keys.me })
}
