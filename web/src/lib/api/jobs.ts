import { query, request } from './client'

export interface JobCounts {
  needs_review: number
  active: number
  succeeded: number
  failed: number
  cancelled: number
  total: number
  /** Per exact status; the task center's cards and filter share this. */
  statuses: Record<string, number>
}

/** A job's credits over its lifetime; null fields are unknown (jobs from the previous engine). */
export interface JobCredits {
  reserved: number
  settled: number
  overage: number | null
  released: number | null
  frozen: number | null
}

export interface JobListItem {
  biz_id: string
  workflow_name: string
  title: string
  status: string
  node_total: number
  node_done: number
  node_failed: number
  credits: JobCredits
  error_code: string
  created_at: string
  started_at: string | null
  finished_at: string | null
  retry_of_job_id: string
  project_id: string
  cover_asset_id: string
  cover_url: string
  cover_type: string
}

export interface JobListFilter {
  bucket?: string
  status?: string
  exclude_status?: string
  workflow?: string
  q?: string
  project_id?: string
  order?: 'oldest'
  cursor?: string
  limit?: number
}

export interface JobNode {
  name: string
  status: string
  executor: string
  outputs: Record<string, unknown> | null
  error: string
  error_code: string
  attempt: number
  queue_reason: string
  /** The step failed and can be run again with the same input. */
  retryable?: boolean
  credit_cost: number
  started_at: string | null
  finished_at: string | null
  /** Builder metadata: result, shot, panel, group. */
  display: Record<string, unknown> | null
}

export interface JobDetail {
  biz_id: string
  workflow_name: string
  title: string
  status: string
  retry_of_job_id: string
  project_id: string
  credits: JobCredits
  review_deadline: string | null
  /** A classic comic whose unfinished panels can be redrawn as a new task. */
  panel_retry?: boolean
  error_code: string
  cover_asset_id: string
  created_at: string
  started_at: string | null
  finished_at: string | null
  nodes: JobNode[]
  spec: Record<string, unknown>
}

export interface QuoteItem {
  kind: string
  count: number
  credits: number
}

export interface ReviewDecision {
  selected_shots: number[]
  redo_shots: number[]
  redo_prompt_overrides: Record<number, string>
}

export interface ReviewQuote {
  items: QuoteItem[]
  total: number
  all_upgrade: number
}

export interface ComicRetryQuote {
  panels: { index: number; text: string }[]
  items: QuoteItem[]
  credits_total: number
}

export const jobsApi = {
  summary: (projectId?: string) => request<JobCounts>('GET', `/jobs/summary${query({ project_id: projectId })}`),
  list: (f: JobListFilter) =>
    request<{ jobs: JobListItem[]; next_cursor?: string }>('GET', `/jobs${query({ ...f })}`),
  rename: (bizId: string, title: string) => request<void>('PATCH', `/jobs/${bizId}`, { title }),
  cancel: (bizId: string) => request<void>('POST', `/jobs/${bizId}/cancel`),
  remove: (bizId: string) => request<void>('DELETE', `/jobs/${bizId}`),
  get: (bizId: string) => request<JobDetail>('GET', `/jobs/${bizId}`),
  quoteReview: (bizId: string, d: ReviewDecision, signal?: AbortSignal) =>
    request<ReviewQuote>('POST', `/jobs/${bizId}/resume/quote`, d, { signal }),
  resume: (bizId: string, d: ReviewDecision & { quote_total: number }) => request<void>('POST', `/jobs/${bizId}/resume`, d),
  quoteComicRetry: (bizId: string) => request<ComicRetryQuote>('POST', `/jobs/${bizId}/panels/retry/quote`, {}),
  retryComicPanels: (bizId: string, texts: Record<number, string>, quoteTotal: number) =>
    request<{ biz_id: string }>('POST', `/jobs/${bizId}/panels/retry`, { texts, quote_total: quoteTotal }, { idempotencyKey: crypto.randomUUID() }),
  retryNode: (bizId: string, node: string) =>
    request<{ biz_id: string }>('POST', `/jobs/${bizId}/nodes/${node}/retry`, {}, { idempotencyKey: crypto.randomUUID() }),
}
