import { useAuthStore } from './authStore'

// PRD §13.1: base /api/v1, JSON error envelope {code,message,request_id}.
const API_BASE = import.meta.env.VITE_API_BASE ?? 'http://127.0.0.1:8080/api/v1'

export class ApiError extends Error {
  code: string
  constructor(code: string, message: string) {
    super(message)
    this.code = code
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  opts: { auth?: boolean; idempotencyKey?: string } = {},
): Promise<T> {
  const { auth = true, idempotencyKey } = opts
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (auth) {
    const token = useAuthStore.getState().accessToken
    if (token) headers.Authorization = `Bearer ${token}`
  }
  // §11.5: lets a retried submission (network timeout, double-click) return
  // the original job instead of double-submitting — see jobsvc.Service.Create.
  if (idempotencyKey) headers['Idempotency-Key'] = idempotencyKey
  const resp = await fetch(`${API_BASE}${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (!resp.ok) {
    const data = await resp.json().catch(() => ({ code: 'unknown', message: resp.statusText }))
    throw new ApiError(data.code ?? 'unknown', data.message ?? resp.statusText)
  }
  if (resp.status === 204) return undefined as T
  return (await resp.json()) as T
}

export interface TokenPair {
  access_token: string
  refresh_token: string
}

export interface MeResponse {
  biz_id: string
  email: string
  balance: number
}

export interface CreateJobResponse {
  biz_id: string
  status: string
  workflow_run_id: string
}

export interface JobNode {
  name: string
  phase: string
  outputs: Record<string, unknown> | null
  error: string
}

export interface JobResponse {
  biz_id: string
  workflow_name: string
  title: string
  status: string
  workflow_run_id: string
  nodes: JobNode[]
  spec: Spec
}

export interface AssetResponse {
  biz_id: string
  type: string
  public_url: string
  mime: string
  width: number
  height: number
}

// CharacterSlot/Spec mirror internal/application/jobsvc.CharacterSlot/Spec's
// JSON shape exactly — kept in sync by hand since there's no shared schema
// generation between the Go and TS sides in this POC.
export interface CharacterSlot {
  slot: string
  character_id: string
}

export interface Spec {
  text?: string
  n?: number
  panels?: string[]
  story?: string // image.comic4 only, F5.4: auto-split into 4 panels instead of panels
  shots?: string[]
  characters?: CharacterSlot[]
  preset_ids?: string[]
  seed?: number
  source_image_asset_id?: string // F5.8, image.single only
  // video.single / video.sequence fields, unused by the image forms.
  duration_seconds?: number
  resolution?: string
  ratio?: string
  first_frame_asset_id?: string
  last_frame_asset_id?: string
  reference_image_asset_ids?: string[]
  reference_video_asset_ids?: string[]
  reference_audio_asset_ids?: string[]
  recalibrate_every?: number
  prompt_enhance?: boolean // F6.10, video.single only
}

export type WorkflowName =
  | 'image.single'
  | 'image.batch'
  | 'image.comic4'
  | 'image.sequence'
  | 'video.single'
  | 'video.sequence'

export interface Character {
  biz_id: string
  name: string
  description: string
  ref_asset_ids: string[]
  seed: number
}

export interface Preset {
  biz_id: string
  category: string
  name: string
  cover_url: string
  prompt_fragment: string
  priority: number
  style_type: string
}

export interface ResumeVideoSequenceRequest {
  selected_shots: number[]
  redo_shots: number[]
  redo_prompt_overrides?: Record<number, string>
}

export const api = {
  register: (email: string, password: string) =>
    request<TokenPair>('POST', '/auth/register', { email, password }, { auth: false }),
  login: (email: string, password: string) =>
    request<TokenPair>('POST', '/auth/login', { email, password }, { auth: false }),
  me: () => request<MeResponse>('GET', '/me'),

  // F1.2: anonymous single-image trial, gated server-side by device_id + IP.
  trialImage: (prompt: string, deviceId: string) =>
    request<{ image_url: string }>(
      'POST',
      '/trial/image',
      { prompt, device_id: deviceId },
      { auth: false },
    ),

  createJob: (workflowName: WorkflowName, spec: Spec, idempotencyKey?: string) =>
    request<CreateJobResponse>(
      'POST',
      '/jobs',
      { workflow_name: workflowName, spec },
      { idempotencyKey },
    ),
  getJob: (bizId: string) => request<JobResponse>('GET', `/jobs/${bizId}`),
  resumeJob: (bizId: string, body: ResumeVideoSequenceRequest) =>
    request<void>('POST', `/jobs/${bizId}/resume`, body),

  getAsset: (bizId: string) => request<AssetResponse>('GET', `/assets/${bizId}`),
  listAssets: (opts: { type?: 'image' | 'video'; limit?: number } = {}) => {
    const params = new URLSearchParams()
    if (opts.type) params.set('type', opts.type)
    if (opts.limit) params.set('limit', String(opts.limit))
    const qs = params.toString()
    return request<{ assets: AssetResponse[] }>('GET', qs ? `/assets?${qs}` : '/assets')
  },
  deleteAsset: (bizId: string) => request<void>('DELETE', `/assets/${bizId}`),
  // Binary zip response, not JSON — bypasses the generic request() helper.
  batchDownloadAssets: async (assetIds: string[]): Promise<Blob> => {
    const token = useAuthStore.getState().accessToken
    const resp = await fetch(`${API_BASE}/assets/batch-download`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({ asset_ids: assetIds }),
    })
    if (!resp.ok) {
      const data = await resp.json().catch(() => ({ code: 'unknown', message: resp.statusText }))
      throw new ApiError(data.code ?? 'unknown', data.message ?? resp.statusText)
    }
    return resp.blob()
  },

  listCharacters: () => request<{ characters: Character[] }>('GET', '/characters'),
  createCharacter: (name: string, description: string, refAssetIds: string[], seed: number) =>
    request<Character>('POST', '/characters', {
      name,
      description,
      ref_asset_ids: refAssetIds,
      seed,
    }),

  listPresets: (category?: string) =>
    request<{ presets: Preset[] }>('GET', category ? `/presets?category=${category}` : '/presets'),
}
