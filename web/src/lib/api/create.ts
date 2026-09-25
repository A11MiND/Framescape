import { query, request } from './client'
import type { QuoteItem } from './jobs'
import type { AssetResponse, Character, Preset } from '../api'

export interface OpenAICapabilities {
  enabled: boolean
  image_model: string
  entitlement: string
  sizes: string[]
  qualities: string[]
  max_n: number
  default_quality: string
  max_references: number
  reference_formats: string[]
  max_reference_bytes: number
}

export interface Capabilities {
  image: { max_n: number; max_prompt_chars: number; sequence_max_shots?: number }
  video: { duration_min: number; duration_max: number; max_prompt_chars: number; resolutions: string[]; ratios: string[]; max_reference_videos?: number; reference_video_max_seconds?: number; sequence_max_shots?: number }
  comic?: { openai_enabled: boolean; model: string; max_composed_chars: number; max_references: number }
  providers?: {
    minimax: { enabled: boolean }
    openai: OpenAICapabilities
    gemini: { enabled: boolean; image_model: string }
  }
}

/** The request body of job creation, estimate and preview. */
export interface CreateRequest {
  workflow_name: string
  spec: Record<string, unknown>
  project_id?: string
  quote_total?: number
}

export interface Estimate {
  credits_total: number
  items: (QuoteItem & { basis?: string })[]
}

export const createApi = {
  capabilities: () => request<Capabilities>('GET', '/capabilities', undefined, { auth: false }),
  estimate: (r: CreateRequest, signal?: AbortSignal) => request<Estimate>('POST', '/jobs/estimate', r, { signal }),
  preview: (r: CreateRequest, signal?: AbortSignal) => request<{ prompt: string; references: string[] }>('POST', '/jobs/preview', r, { signal }),
  create: (r: CreateRequest, idempotencyKey: string) => request<{ biz_id: string; status: string }>('POST', '/jobs', r, { idempotencyKey }),
  rewrite: (text: string) => request<{ text: string }>('POST', '/prompts/rewrite', { text }),
  trial: (prompt: string, deviceId: string) => request<{ image_url: string }>('POST', '/trial/image', { prompt, device_id: deviceId }, { auth: false }),
  characters: () => request<{ characters: Character[] }>('GET', '/characters'),
  presets: () => request<{ presets: Preset[] }>('GET', '/presets'),
  recentAssets: (type: 'image' | 'video' | 'audio', limit = 24) => request<{ assets: AssetResponse[] }>('GET', `/assets${query({ type, limit })}`),
}
