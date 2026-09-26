import { query, request } from './client'
import type { CommunityStreak } from '../api'

export type { CommunityStreak }

export interface CommunityWork {
  biz_id: string
  type: 'image' | 'video'
  public_url: string
  thumb_url?: string
  width: number
  height: number
  duration_ms?: number
  resolution_tag: string
  published_at: string
  prompt?: string
  like_count: number
  liked: boolean
  mine?: boolean
}

export interface FeedFilter {
  type?: 'image' | 'video'
  mine?: boolean
}

export const communityApi = {
  feed: (f: FeedFilter, cursor?: string) =>
    request<{ assets: CommunityWork[]; next_cursor?: string }>('GET', `/community/feed${query({ type: f.type, mine: f.mine ? 1 : undefined, cursor, limit: 40 })}`),
  streak: () => request<CommunityStreak>('GET', '/community/streak'),
  like: (id: string) => request<{ liked: boolean; like_count: number }>('POST', `/assets/${id}/like`),
  unlike: (id: string) => request<{ liked: boolean; like_count: number }>('DELETE', `/assets/${id}/like`),
  unpublish: (id: string) => request<void>('PATCH', `/assets/${id}`, { is_public: false }),
}
