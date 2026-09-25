import { request } from './client'

export interface Me {
  biz_id: string
  email: string | null
  phone: string | null
  balance: number
  /** Credits reserved by running tasks. */
  held: number
  is_admin: boolean
  entitlements: string[]
}

export const accountApi = {
  me: () => request<Me>('GET', '/me'),
}

export function hasOpenAIImage(me: Me | undefined): boolean {
  return Boolean(me && (me.is_admin || me.entitlements.includes('openai_image')))
}
