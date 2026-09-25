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

export const jobsApi = {
  summary: (projectId?: string) => request<JobCounts>('GET', `/jobs/summary${query({ project_id: projectId })}`),
}
