import { query, request, API_BASE, ApiError } from './client'
import { useAuthStore } from '../authStore'

export interface AssetInfo {
  biz_id: string
  type: 'image' | 'video' | 'audio' | string
  public_url: string
  thumb_url: string
  mime: string
  duration_ms: number
  width: number
  height: number
  resolution_tag: string
  created_at: string
  project_id: string
  is_public: boolean
  prompt: string
  /** Detail only: upload or generation, the generation parameters and the source task. */
  source?: string
  meta?: Record<string, unknown> | null
  job_biz_id?: string
}

export interface TrashedAsset extends AssetInfo {
  deleted_at: string
  days_until_purge: number
}

export interface AssetFilter {
  type?: string
  project_id?: string
  q?: string
}

export const assetsApi = {
  get: (bizId: string) => request<AssetInfo>('GET', `/assets/${bizId}`),
  setPublic: (bizId: string, isPublic: boolean) => request<void>('PATCH', `/assets/${bizId}`, { is_public: isPublic }),
  setProject: (bizId: string, projectId: string) =>
    request<void>('PATCH', `/assets/${bizId}`, projectId ? { project_id: projectId } : { clear_project: true }),
  list: (f: AssetFilter, cursor?: string) =>
    request<{ assets: AssetInfo[]; next_cursor?: string }>('GET', `/assets${query({ ...f, limit: 60, cursor })}`),
  trash: () => request<{ assets: TrashedAsset[]; total: number }>('GET', '/assets/trash'),
  batch: (op: 'delete' | 'restore' | 'move', ids: string[], projectId?: string) =>
    request<{ affected: number }>('POST', '/assets/batch', { op, ids, ...(op === 'move' ? { project_id: projectId ?? '' } : {}) }),
  emptyTrash: () => request<{ purged: number }>('POST', '/assets/trash/empty'),
  /** A zip of the given assets; a binary response, so not through request(). */
  download: async (ids: string[]): Promise<Blob> => {
    const token = useAuthStore.getState().accessToken
    const resp = await fetch(`${API_BASE}/assets/batch-download`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
      body: JSON.stringify({ asset_ids: ids }),
    })
    if (!resp.ok) throw new ApiError('download_failed', 'download failed', resp.status)
    return resp.blob()
  },
}
