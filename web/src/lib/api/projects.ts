import { request } from './client'

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
}
