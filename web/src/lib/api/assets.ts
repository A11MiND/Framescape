import { request } from './client'

export interface AssetInfo {
  biz_id: string
  type: 'image' | 'video' | string
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
}

export const assetsApi = {
  get: (bizId: string) => request<AssetInfo>('GET', `/assets/${bizId}`),
  setPublic: (bizId: string, isPublic: boolean) => request<void>('PATCH', `/assets/${bizId}`, { is_public: isPublic }),
}
