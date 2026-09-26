import { query, request } from './client'

export interface ProjectSummary {
  biz_id: string
  name: string
  description: string
  created_at: string
  asset_count: number
  job_count: number
  character_count: number
  last_activity_at: string | null
  cover_urls: string[]
}

export const projectsApi = {
  list: () => request<{ projects: ProjectSummary[] }>('GET', '/projects'),
  search: (q: string) => request<{ projects: ProjectSummary[] }>('GET', `/projects${query({ q: q || undefined })}`),
  get: (id: string) => request<ProjectSummary>('GET', `/projects/${id}`),
  create: (name: string, description: string) => request<ProjectSummary>('POST', '/projects', { name, description }),
  update: (id: string, name: string, description: string) => request<ProjectSummary>('PATCH', `/projects/${id}`, { name, description }),
  remove: (id: string) => request<{ detached: { assets: number; jobs: number; characters: number } }>('DELETE', `/projects/${id}`),
}
