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

export const jobsApi = {
  summary: (projectId?: string) => request<JobCounts>('GET', `/jobs/summary${query({ project_id: projectId })}`),
  list: (f: JobListFilter) =>
    request<{ jobs: JobListItem[]; next_cursor?: string }>('GET', `/jobs${query({ ...f })}`),
  rename: (bizId: string, title: string) => request<void>('PATCH', `/jobs/${bizId}`, { title }),
  cancel: (bizId: string) => request<void>('POST', `/jobs/${bizId}/cancel`),
  remove: (bizId: string) => request<void>('DELETE', `/jobs/${bizId}`),
}
