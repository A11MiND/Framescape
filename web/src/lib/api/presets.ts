import { request } from './client'
import type { Preset } from '../api'

export type { Preset }

export const PRESET_CATEGORIES = ['style', 'pose', 'composition', 'lighting', 'camera'] as const
export type PresetCategory = (typeof PRESET_CATEGORIES)[number]

export interface PresetInput {
  name: string
  prompt_fragment: string
  category: string
  style_type: string
}

export const presetsApi = {
  list: () => request<{ presets: Preset[] }>('GET', '/presets'),
  create: (body: PresetInput) => request<Preset>('POST', '/presets', body),
  update: (id: string, body: Partial<PresetInput>) => request<Preset>('PATCH', `/presets/${id}`, body),
  remove: (id: string) => request<void>('DELETE', `/presets/${id}`),
}
