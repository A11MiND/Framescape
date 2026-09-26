import { request } from './client'
import type { Character } from '../api'

export type { Character }

export interface CharacterInput {
  name: string
  description: string
  ref_asset_ids: string[]
  project_id?: string
  seed?: number
}

export const charactersApi = {
  list: () => request<{ characters: Character[] }>('GET', '/characters'),
  create: (body: CharacterInput) => request<Character>('POST', '/characters', body),
  update: (id: string, body: Partial<CharacterInput> & { clear_project?: boolean }) => request<Character>('PATCH', `/characters/${id}`, body),
  remove: (id: string) => request<void>('DELETE', `/characters/${id}`),
}
