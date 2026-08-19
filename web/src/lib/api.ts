import { useAuthStore } from './authStore'
import i18n from '../i18n'

// PRD §13.1: base /api/v1, JSON error envelope {code,message,request_id}.
const API_BASE = import.meta.env.VITE_API_BASE ?? 'http://127.0.0.1:8080/api/v1'

export class ApiError extends Error {
  code: string
  status: number
  constructor(code: string, message: string, status: number) {
    super(message)
    this.code = code
    this.status = status
  }
}

// A 401 mid-session means the 7-day access token (F1.1) expired, not that
// the user did anything wrong — refreshToken has sat unused in localStorage
// since login otherwise. This refreshes it once and replays the original
// request rather than surfacing an error the user can't act on. Concurrent
// 401s (several in-flight requests when the token expires) share one
// refresh call instead of each firing their own.
let refreshPromise: Promise<boolean> | null = null

async function ensureFreshToken(): Promise<boolean> {
  const { refreshToken, setTokens, logout } = useAuthStore.getState()
  if (!refreshToken) return false
  if (!refreshPromise) {
    refreshPromise = fetch(`${API_BASE}/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
      .then(async (resp) => {
        if (!resp.ok) throw new Error('refresh failed')
        const data = (await resp.json()) as TokenPair
        setTokens(data.access_token, data.refresh_token)
        return true
      })
      .catch(() => {
        logout()
        return false
      })
      .finally(() => {
        refreshPromise = null
      })
  }
  return refreshPromise
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  opts: { auth?: boolean; idempotencyKey?: string; _retried?: boolean } = {},
): Promise<T> {
  const { auth = true, idempotencyKey, _retried = false } = opts
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
  if (resp.status === 401 && auth && !_retried && path !== '/auth/refresh') {
    if (await ensureFreshToken()) return request<T>(method, path, body, { ...opts, _retried: true })
  }
  if (!resp.ok) {
    const data = await resp.json().catch(() => ({ code: 'unknown', message: resp.statusText }))
    throw new ApiError(data.code ?? 'unknown', data.message ?? resp.statusText, resp.status)
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
  // null for a phone-only account (no email set) — see auth_oauth.go.
  email: string | null
  balance: number
}

export interface Capabilities {
  image: { max_n: number; max_prompt_chars: number }
  video: {
    duration_min: number
    duration_max: number
    max_prompt_chars: number
    resolutions: string[]
    ratios: string[]
  }
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
  // -1 outside a Loop iteration — lets image.comic4's 4 gen-one-panel
  // iterations (which all share that one node name) show each panel's own
  // status instead of one aggregated "k/n done" box. image.sequence doesn't
  // need this anymore: each shot is its own distinctly-named "shot-N" DAG
  // task (image_sequence.go's own doc), so loop_index is always -1 there.
  loop_index: number
  // Only present once this specific task_run_id has a matching row in the
  // job_nodes projection table (handleGetJob's own doc) — absent for a
  // node the projector hasn't seen yet, not just zero/empty.
  credit_cost?: number
  started_at?: string | null
  finished_at?: string | null
}

export interface JobResponse {
  biz_id: string
  workflow_name: string
  title: string
  status: string
  workflow_run_id: string
  nodes: JobNode[]
  spec: Spec
  retry_of_job_id: string
  project_id: string
  credit_estimated: number
  credit_held: number
  credit_settled: number
}

export interface AssetResponse {
  biz_id: string
  type: string
  public_url: string
  mime: string
  width: number
  height: number
  resolution_tag: string
  created_at: string
  // "" when unassigned — never null/undefined, matches assetToJSON's own
  // always-present-string convention (Go's projectBizID resolves to "").
  project_id: string
  is_public: boolean
  // Only present on the single-asset GET (assetDetailJSON), never on
  // listAssets' lean per-row projection — see that handler's own doc.
  source?: string
  meta?: Record<string, unknown>
  job_biz_id?: string
}

// TrashAsset is handleListTrash's own projection (assetToJSON's fields
// minus project_id, which means nothing once an asset is in the recycle
// bin) plus when it was deleted and how many days until autoPurgeTrash
// actually removes it (upkeep.TrashRetentionDays).
export interface TrashAsset {
  biz_id: string
  type: string
  public_url: string
  width: number
  height: number
  resolution_tag: string
  deleted_at: string
  days_until_purge: number
}

// CommunityAsset is handleCommunityFeed's own minimal projection — not
// AssetResponse, deliberately: a viewer here isn't the owner, so this never
// carries project_id, full meta, or anything else owner-specific.
export interface CommunityAsset {
  biz_id: string
  type: string
  public_url: string
  width: number
  height: number
  resolution_tag: string
  published_at: string
  prompt?: string
}

// StreakMilestone/CommunityStreak mirror handleCommunityStreak's response —
// communitysvc.Service.GetStatus's own doc covers the milestone/cap rules
// this is just a read projection of.
export interface StreakMilestone {
  days: number
  credits: number
  monthly_cap: number
  used_this_month: number
}

export interface CommunityStreak {
  current_streak: number
  milestones: StreakMilestone[]
}

export interface Project {
  biz_id: string
  name: string
  description: string
  created_at: string
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
  n?: number // image.single only: also image.comic4's own auto-split panel count when no explicit panels are given (default 4)
  panels?: string[] // image.comic4 only: 2..9 panel prompts, always chained panel-to-panel — no independent "quick" mode anymore
  story?: string // image.comic4 only, F5.4: auto-split into n panels (default 4) instead of panels
  shots?: string[]
  shot_source_refs?: number[] // image.sequence only — cross-shot referencing, see jobsvc.go's Spec.ShotSourceRefs doc
  image_sequence_mode?: 'quick' | 'continuity' // image.sequence only — see jobsvc.go's Spec.ImageSequenceMode doc
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
  skip_preview?: boolean // video.sequence only — draft runs directly at 2K, no 768P preview gate
  source_video_asset_id?: string // video.sequence only — r2va anchor when the reference is a video, not an image
  prompt_enhance?: boolean // F6.10, video.single only

  // video.sequence's narrative-continuity feature (see jobsvc.go's
  // Spec.NarrativeContinuity doc) — off by default, unchanged behavior.
  narrative_continuity?: boolean
  reference_selection_mode?: 'window' | 'manual' | 'smart'
  shot_reference_overrides?: number[] // video.sequence's own #-mention override, same shape as image.sequence's shot_source_refs
}

export type WorkflowName =
  | 'image.single' // covers what used to be the separate image.batch workflow — n is just an optional field now
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
  project_id?: string
}

export interface Preset {
  biz_id: string
  category: string
  name: string
  // name_en is '' for every user-saved "mine" preset (free text, never
  // auto-translated) — presetDisplayName() is what actually falls back to
  // `name` when this is empty, callers should use that instead of reading
  // name_en directly.
  name_en: string
  cover_url: string
  prompt_fragment: string
  priority: number
  style_type: string
  // mine: whether owner_user_id is the caller's (F4.5's "另存為我的預設") —
  // false for every seeded system preset. Only presets with mine:true are
  // ever eligible for DELETE (handleDeletePreset's own doc).
  mine: boolean
}

export function presetDisplayName(preset: Preset, lang: string): string {
  return lang === 'en' && preset.name_en ? preset.name_en : preset.name
}

// kind is a machine-readable constant (jobsvc.ItemKind* on the Go side),
// not a human-readable label — see EstimateItem's own Go doc for why: the
// backend never renders locale-specific text, the frontend maps kind to a
// translated string via studio.breakdown.item.<kind>.
export interface EstimateItem {
  kind: string
  count: number
  credits: number
}

export interface ResumeVideoSequenceRequest {
  selected_shots: number[]
  redo_shots: number[]
  redo_prompt_overrides?: Record<number, string>
}

// JobSummary is GET /jobs's per-row shape (F7.1) — a lighter projection than
// JobResponse (no nodes[]/spec, GET /jobs/{bizID} is still where those come
// from), plus the credit/progress counters the list view needs that the
// detail endpoint never had to expose.
export interface JobSummary {
  biz_id: string
  workflow_name: string
  title: string
  status: string
  node_total: number
  node_done: number
  node_failed: number
  credit_estimated: number
  credit_held: number
  credit_settled: number
  created_at: string
  finished_at: string | null
  // "" when this job wasn't submitted by jobsvc.RetryNode — see that
  // handler's own doc for why titles carry no language-specific prefix
  // and this field is the real (locale-agnostic) retry-provenance signal.
  retry_of_job_id: string
}

// remark.kind is a machine-readable constant (creditsvc's own
// remarkPayload.Kind on the Go side) — same split as EstimateItem.kind, the
// frontend maps it to a translated string via credits.remark.<kind>. kind
// is "" for ledger rows written before this existed (a bare English
// sentence lives in `text` for those, shown verbatim as a fallback) and for
// recharge_custom (operator-supplied CLI text, inherently unlocalizable).
export interface CreditRemark {
  kind: string
  amount: number
  workflow: string
  cost_yuan: number
  text: string
}

export interface CreditLedgerEntry {
  direction: string
  amount: number
  balance_after: number
  held_after: number
  ref_type: string
  ref_id: string
  remark: CreditRemark
  created_at: string
}

export const api = {
  // code is ignored server-side unless config.EmailProviderAPIKey() is set
  // (handleRegister's own doc) — harmless to always send whatever the
  // "发送验证码" field holds, empty or not.
  register: (email: string, password: string, code?: string) =>
    request<TokenPair>('POST', '/auth/register', { email, password, code }, { auth: false }),
  login: (email: string, password: string) =>
    request<TokenPair>('POST', '/auth/login', { email, password }, { auth: false }),
  // auth_oauth.go's own doc — all three answer 400 with code
  // "google_not_configured" / "sms_not_configured" / "email_not_configured"
  // until real provider credentials exist server-side.
  googleLogin: (idToken: string) => request<TokenPair>('POST', '/auth/google', { id_token: idToken }, { auth: false }),
  sendPhoneCode: (phone: string) => request<void>('POST', '/auth/phone/send-code', { phone }, { auth: false }),
  sendEmailCode: (email: string) => request<void>('POST', '/auth/email/send-code', { email }, { auth: false }),
  verifyPhoneCode: (phone: string, code: string) =>
    request<TokenPair>('POST', '/auth/phone/verify', { phone, code }, { auth: false }),
  me: () => request<MeResponse>('GET', '/me'),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<void>('PATCH', '/me/password', { current_password: currentPassword, new_password: newPassword }),
  rewritePrompt: (text: string) => request<{ text: string }>('POST', '/prompts/rewrite', { text }),

  // PRD §10.5/§13.2's Capability Matrix — public (no auth), so it loads
  // before login same as the trial below. Studio fetches this once and
  // uses it to drive its duration/resolution/ratio <select> options
  // instead of hardcoding them a second time client-side.
  getCapabilities: () => request<Capabilities>('GET', '/capabilities', undefined, { auth: false }),

  // F1.2: anonymous single-image trial, gated server-side by device_id + IP.
  trialImage: (prompt: string, deviceId: string) =>
    request<{ image_url: string }>(
      'POST',
      '/trial/image',
      { prompt, device_id: deviceId },
      { auth: false },
    ),

  createJob: (workflowName: WorkflowName, spec: Spec, idempotencyKey?: string, projectId?: string) =>
    request<CreateJobResponse>(
      'POST',
      '/jobs',
      { workflow_name: workflowName, spec, project_id: projectId || undefined },
      { idempotencyKey },
    ),
  getJob: (bizId: string) => request<JobResponse>('GET', `/jobs/${bizId}`),
  resumeJob: (bizId: string, body: ResumeVideoSequenceRequest) =>
    request<void>('POST', `/jobs/${bizId}/resume`, body),
  cancelJob: (bizId: string) => request<void>('POST', `/jobs/${bizId}/cancel`),
  // Only terminal jobs (succeeded/failed/cancelled) can be deleted — the
  // backend soft-deletes (jobsvc.Service.Delete's own doc).
  deleteJob: (bizId: string) => request<void>('DELETE', `/jobs/${bizId}`),
  // Node-retry: supported for image.comic4's gen-one-panel and video.single's
  // gen (loopIndex -1 for that last one — it's not a loop iteration) —
  // jobsvc.RetryNode's own doc covers why video.sequence's and
  // image.sequence's per-shot nodes don't fit (each shot is its own
  // distinctly-named DAG task whose inputs can depend on another shot's
  // runtime output, not a single leaf task reconstructible from Spec alone).
  // Returns a brand new satellite job, not a patch to bizId's own run.
  retryNode: (bizId: string, nodeName: string, loopIndex: number, promptOverride?: string) =>
    request<CreateJobResponse>('POST', `/jobs/${bizId}/nodes/${nodeName}/retry`, {
      loop_index: loopIndex,
      prompt_override: promptOverride || undefined,
    }),

  // F7.1: newest-first, optional status filter, cursor pagination (see
  // jobsvc.Service.List's doc for the cursor shape — a decreasing numeric id).
  listJobs: (opts: { status?: string; cursor?: string; limit?: number; projectId?: string } = {}) => {
    const params = new URLSearchParams()
    if (opts.status) params.set('status', opts.status)
    if (opts.cursor) params.set('cursor', opts.cursor)
    if (opts.limit) params.set('limit', String(opts.limit))
    if (opts.projectId) params.set('project_id', opts.projectId)
    const qs = params.toString()
    return request<{ jobs: JobSummary[]; next_cursor?: string }>('GET', qs ? `/jobs?${qs}` : '/jobs')
  },

  // §13.3: quotes the credit figure a submission with this exact
  // workflow_name/spec would hold, without holding anything — Studio calls
  // this (debounced) instead of computing the number itself, so pricing
  // logic has exactly one home (jobsvc.EstimateCredits) instead of two that
  // can drift.
  estimateJob: (workflowName: WorkflowName, spec: Spec) =>
    request<{ credits_total: number; items: EstimateItem[] }>('POST', '/jobs/estimate', {
      workflow_name: workflowName,
      spec,
    }),

  getAsset: (bizId: string) => request<AssetResponse>('GET', `/assets/${bizId}`),
  listAssets: (opts: { type?: 'image' | 'video'; projectId?: string; limit?: number; q?: string; isPublic?: boolean } = {}) => {
    const params = new URLSearchParams()
    if (opts.type) params.set('type', opts.type)
    if (opts.projectId) params.set('project_id', opts.projectId)
    if (opts.limit) params.set('limit', String(opts.limit))
    if (opts.q) params.set('q', opts.q)
    if (opts.isPublic) params.set('is_public', 'true')
    const qs = params.toString()
    return request<{ assets: AssetResponse[] }>('GET', qs ? `/assets?${qs}` : '/assets')
  },
  deleteAsset: (bizId: string) => request<void>('DELETE', `/assets/${bizId}`),
  listTrash: () => request<{ assets: TrashAsset[] }>('GET', '/assets/trash'),
  restoreAsset: (bizId: string) => request<void>('POST', `/assets/${bizId}/restore`),
  // Permanent, unrecoverable — handleEmptyTrash's own doc. The one place in
  // this app that should ask for confirmation before firing, since every
  // other delete here is a reversible soft-delete.
  emptyTrash: () => request<{ purged: number }>('POST', '/assets/trash/empty'),
  // projectId omitted (undefined) clears the assignment (clear_project:true) —
  // there's no "leave unchanged" case here since this call always means
  // "the user picked something in the project selector".
  setAssetProject: (bizId: string, projectId?: string) =>
    request<void>('PATCH', `/assets/${bizId}`, projectId ? { project_id: projectId } : { clear_project: true }),
  setAssetPublic: (bizId: string, isPublic: boolean) =>
    request<void>('PATCH', `/assets/${bizId}`, { is_public: isPublic }),
  listCommunityFeed: (limit?: number) =>
    request<{ assets: CommunityAsset[] }>('GET', limit ? `/community/feed?limit=${limit}` : '/community/feed'),
  getCommunityStreak: () => request<CommunityStreak>('GET', '/community/streak'),

  listProjects: () => request<{ projects: Project[] }>('GET', '/projects'),
  createProject: (name: string, description: string) =>
    request<Project>('POST', '/projects', { name, description }),
  updateProject: (bizId: string, body: Partial<Pick<Project, 'name' | 'description'>>) =>
    request<Project>('PATCH', `/projects/${bizId}`, body),
  deleteProject: (bizId: string) => request<void>('DELETE', `/projects/${bizId}`),

  // F2.1's presigned direct-upload pair. getUploadURL never touches object
  // storage itself (that's cmd/api's job); uploadToPresignedURL does, via a
  // raw fetch bypassing request() entirely — different host, no JSON body,
  // no auth header, same reasoning as batchDownloadAssets' own raw fetch
  // below. See web/src/lib/upload.ts for the orchestration (presign → PUT →
  // read local dimensions → complete) that ties these two together.
  getUploadURL: (filename: string, mime: string) =>
    request<{ biz_id: string; upload_url: string; storage_key: string }>('POST', '/assets/upload-url', {
      filename,
      mime,
    }),
  uploadToPresignedURL: async (url: string, file: File): Promise<void> => {
    const resp = await fetch(url, { method: 'PUT', headers: { 'Content-Type': file.type }, body: file })
    if (!resp.ok) throw new ApiError('upload_failed', i18n.t('api.uploadFailed', { status: resp.status }), resp.status)
  },
  completeAsset: (bizId: string, body: { storage_key: string; width?: number; height?: number; duration_ms?: number }) =>
    request<AssetResponse>('POST', `/assets/${bizId}/complete`, body),
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
      throw new ApiError(data.code ?? 'unknown', data.message ?? resp.statusText, resp.status)
    }
    return resp.blob()
  },

  listCharacters: (opts: { projectId?: string } = {}) => {
    const qs = opts.projectId ? `?project_id=${opts.projectId}` : ''
    return request<{ characters: Character[] }>('GET', `/characters${qs}`)
  },
  createCharacter: (name: string, description: string, refAssetIds: string[], seed: number, projectId?: string) =>
    request<Character>('POST', '/characters', {
      name,
      description,
      ref_asset_ids: refAssetIds,
      seed,
      project_id: projectId || undefined,
    }),
  // updateCharacter is a partial PATCH — only the fields present in body are
  // touched server-side (handleUpdateCharacter's own doc), so callers only
  // need to pass what actually changed.
  updateCharacter: (
    bizId: string,
    body: {
      name?: string
      description?: string
      ref_asset_ids?: string[]
      seed?: number
      project_id?: string
      clear_project?: boolean
    },
  ) => request<Character>('PATCH', `/characters/${bizId}`, body),
  deleteCharacter: (bizId: string) => request<void>('DELETE', `/characters/${bizId}`),

  listPresets: (category?: string) =>
    request<{ presets: Preset[] }>('GET', category ? `/presets?category=${category}` : '/presets'),
  // F4.5's "另存為我的預設" — always lands with owner_user_id = caller, see
  // handleCreatePreset's own doc.
  createPreset: (body: { name: string; prompt_fragment: string; category?: string; style_type?: string }) =>
    request<Preset>('POST', '/presets', body),
  deletePreset: (bizId: string) => request<void>('DELETE', `/presets/${bizId}`),

  // F1.3: balance/held plus the ledger rows that produced them.
  creditsBalance: () => request<{ balance: number; held: number }>('GET', '/credits/balance'),
  // POC-only demo top-up — see handleCreditsTopup's own doc for why this
  // isn't a real payment flow.
  creditsTopup: () => request<{ balance: number; held: number; credited: number }>('POST', '/credits/topup'),
  creditsLedger: (opts: { cursor?: string; limit?: number } = {}) => {
    const params = new URLSearchParams()
    if (opts.cursor) params.set('cursor', opts.cursor)
    if (opts.limit) params.set('limit', String(opts.limit))
    const qs = params.toString()
    return request<{ entries: CreditLedgerEntry[]; next_cursor?: string }>(
      'GET',
      qs ? `/credits/ledger?${qs}` : '/credits/ledger',
    )
  },
}
