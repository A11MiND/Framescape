import { request } from './client'

export interface Me {
  avatar_url?: string
  biz_id: string
  email: string | null
  phone: string | null
  balance: number
  /** Credits reserved by running tasks. */
  held: number
  is_admin: boolean
  entitlements: string[]
  /** False for phone- and Google-only accounts. */
  has_password?: boolean
}

export const accountApi = {
  setAvatar: (asset_id: string | null) => request<Me>('PATCH', '/me/avatar', { asset_id }),
  me: () => request<Me>('GET', '/me'),
  changePassword: (current: string, next: string) => request<void>('PATCH', '/me/password', { current_password: current, new_password: next }),
}

export function hasOpenAIImage(me: Me | undefined): boolean {
  return Boolean(me && (me.is_admin || (me.entitlements ?? []).includes('openai_image')))
}
