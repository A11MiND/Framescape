import type { JobListFilter } from '../../lib/api/jobs'

// The task center's filter lives in the URL (shareable, back button works)
// with the API's own parameter names.

export const BUCKETS = ['needs_review', 'active', 'succeeded', 'failed'] as const
export const OTHER_STATUSES = ['queued', 'partial', 'cancelling', 'cancelled'] as const
export const WORKFLOWS = ['image.single', 'image.sequence', 'image.comic4', 'video.single', 'video.sequence'] as const

export interface TaskView {
  bucket?: string
  status?: string
  project?: string
  workflow?: string
  q?: string
  oldest: boolean
}

export function readView(params: URLSearchParams): TaskView {
  const bucket = params.get('bucket') ?? undefined
  const status = params.get('status') ?? undefined
  return {
    bucket: bucket && (BUCKETS as readonly string[]).includes(bucket) ? bucket : undefined,
    status: !bucket && status && (OTHER_STATUSES as readonly string[]).includes(status) ? status : undefined,
    project: params.get('project') ?? undefined,
    workflow: params.get('workflow') ?? undefined,
    q: params.get('q') ?? undefined,
    oldest: params.get('order') === 'oldest',
  }
}

export function writeView(v: TaskView): URLSearchParams {
  const p = new URLSearchParams()
  if (v.bucket) p.set('bucket', v.bucket)
  if (v.status) p.set('status', v.status)
  if (v.project) p.set('project', v.project)
  if (v.workflow) p.set('workflow', v.workflow)
  if (v.q) p.set('q', v.q)
  if (v.oldest) p.set('order', 'oldest')
  return p
}

/** "All" shows the tasks waiting for a decision pinned above the rest. */
export function pinsNeedsReview(v: TaskView): boolean {
  return !v.bucket && !v.status
}

export function listFilter(v: TaskView): JobListFilter {
  return {
    bucket: v.bucket,
    status: v.status,
    exclude_status: pinsNeedsReview(v) ? 'awaiting_review' : undefined,
    project_id: v.project,
    workflow: v.workflow,
    q: v.q,
    order: v.oldest ? 'oldest' : undefined,
    limit: 20,
  }
}

export function isFiltered(v: TaskView): boolean {
  return Boolean(v.bucket || v.status || v.project || v.workflow || v.q)
}

export const ACTIVE_STATUSES = ['queued', 'running', 'awaiting_review'] as const
export const TERMINAL_STATUSES = ['succeeded', 'partial', 'failed', 'cancelled'] as const
