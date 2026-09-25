// A fake API with sample data matching the design boards, for screenshot
// acceptance (demo mode, VITE_DEMO=1) and Playwright tests. Pure: images
// are resolved by the caller; state lives in the returned instance.

export interface DemoResponse {
  status: number
  body?: unknown
  /** Keep the connection open with no data (the event stream). */
  hang?: boolean
}

/** Resolves a sample file name (with extension) to a URL. */
type Img = (file: string) => string

interface Job {
  biz_id: string
  id: number
  workflow_name: string
  title: string
  status: string
  node_total: number
  node_done: number
  node_failed: number
  reserved: number
  settled: number
  error_code: string
  created_at: string
  project_id: string
  cover: string
  /** Demo tasks created in the session finish a few seconds after creation. */
  createdMs?: number
  assets?: string[]
  spec?: Record<string, unknown>
}

const T = (d: string) => `2026-09-${d}:00Z`

function sampleJobs(): Job[] {
  const j = (id: number, workflow: string, title: string, status: string, done: number, total: number, reserved: number, settled: number, created: string, cover: string, extra: Partial<Job> = {}): Job => ({
    biz_id: `J${String(id).padStart(3, '0')}`,
    id,
    workflow_name: workflow,
    title,
    status,
    node_total: total,
    node_done: done,
    node_failed: 0,
    reserved,
    settled,
    error_code: '',
    created_at: T(created),
    project_id: 'P1',
    cover,
    ...extra,
  })
  return [
    j(13, 'image.single', '阳光下的海岸电车', 'succeeded', 1, 1, 40, 36, '24T06:20', 'tram-1'),
    j(12, 'video.sequence', '海岸电车之旅 · 宣传视频', 'awaiting_review', 6, 7, 120, 64, '24T06:10', 'tram-1'),
    j(11, 'video.sequence', '雪山晨雾 · 风格测试', 'awaiting_review', 3, 4, 90, 48, '24T05:02', 'alpine'),
    j(10, 'image.single', '午后阳光下的猫咪', 'succeeded', 4, 4, 20, 18, '23T11:32', 'cat'),
    j(9, 'image.single', '雪山湖泊全景', 'running', 1, 4, 80, 20, '23T08:11', 'alpine'),
    j(8, 'image.sequence', '人物写真 · 氛围系列', 'running', 3, 4, 60, 45, '22T03:03', 'portrait'),
    j(7, 'video.single', '城市夜景短片', 'failed', 0, 2, 100, 0, '22T01:18', 'night', { error_code: 'moderation', node_failed: 1 }),
    j(6, 'image.single', '海湾帆船 · 概念图', 'succeeded', 4, 4, 30, 28, '21T12:45', 'bay'),
    j(5, 'image.comic4', '柠檬汽水四格漫画', 'partial', 3, 4, 40, 31, '20T09:30', 'lemon', { node_failed: 1, error_code: 'provider_busy' }),
    j(4, 'image.single', '日落时分的海面', 'queued', 0, 1, 8, 0, '20T08:00', 'sunset', { project_id: 'P2' }),
    j(3, 'video.single', '海岸电车 · 5 秒镜头', 'cancelled', 1, 2, 50, 10, '19T10:00', 'tram-2', { project_id: 'P2' }),
  ]
}

const BUCKET: Record<string, string[]> = {
  needs_review: ['awaiting_review'],
  active: ['queued', 'running', 'cancelling'],
  succeeded: ['succeeded'],
  failed: ['failed', 'partial'],
  cancelled: ['cancelled'],
}
const STATUSES = ['queued', 'running', 'awaiting_review', 'succeeded', 'partial', 'failed', 'cancelling', 'cancelled']

const IMAGES = ['tram-1', 'tram-2', 'tram-3', 'tram-4', 'tram-hero', 'cat', 'sunset', 'lemon', 'bay', 'night', 'alpine', 'portrait']
const CLIPS = ['clip-1', 'clip-2', 'clip-3']

const SPECS: Record<string, Record<string, unknown>> = {
  J013: { text: '在阳光明媚的海滨城市，一辆红色的有轨电车行驶在靠海的街道上，远处是蓝色的大海和山城。', n: 4, aspect_ratio: '4:3' },
  J012: { shots: ['电车沿着海岸缓缓驶入画面，阳光洒在车身上。', '海边咖啡馆的露台，俯瞰平静的海湾。', '日落时分的海岸小镇，金色的阳光洒在海面上。'], duration_seconds: 5, ratio: '16:9' },
  J007: { text: '霓虹闪烁的城市夜景，镜头缓缓推进。', duration_seconds: 5, ratio: '16:9' },
  J009: { text: '雪山倒映在平静的湖面上，清晨的薄雾。', n: 4 },
}

function liveStatus(x: Job): Job {
  if (x.createdMs && x.status === 'running' && Date.now() - x.createdMs > 3000) {
    x.status = 'succeeded'
    x.node_done = x.node_total
    x.settled = Math.round(x.reserved * 0.9)
  }
  return x
}

function nodesFor(x: Job): unknown[] {
  const node = (name: string, status: string, extra: Record<string, unknown> = {}) => ({
    name, status, executor: '', outputs: null, error: '', error_code: '', attempt: 1, queue_reason: '', credit_cost: 0,
    started_at: x.created_at, finished_at: status === 'succeeded' ? x.created_at : null, display: null, ...extra,
  })
  if (x.createdMs) {
    return [node('gen', x.status === 'succeeded' ? 'succeeded' : 'running', { display: { result: true }, outputs: x.status === 'succeeded' ? { 'asset-ids': x.assets, 'asset-id': x.assets?.[0] } : null })]
  }
  if (x.biz_id === 'J013') return [node('gen', 'succeeded', { outputs: { 'asset-ids': ['tram-1', 'tram-2', 'tram-3', 'tram-4'], 'asset-id': 'tram-1' }, display: { result: true } })]
  if (x.biz_id === 'J012') {
    const shots = [1, 2, 3].flatMap((i) => [
      node(`shot-${i}`, 'succeeded', { outputs: { 'asset-id': `clip-${i}` }, display: { shot: i, group: 'draft' } }),
      node(`shot-${i}-extract`, 'succeeded', { display: { shot: i } }),
    ])
    return [...shots, node('gate', 'suspended')]
  }
  if (x.biz_id === 'J007') return [node('gen', 'failed', { error_code: 'moderation', display: { result: true } })]
  if (x.status === 'running' || x.status === 'queued') return [node('gen', x.status === 'queued' ? 'ready' : 'running', { display: { result: true }, queue_reason: x.status === 'queued' ? 'capacity' : '' })]
  if (x.status === 'succeeded') return [node('gen', 'succeeded', { outputs: { 'asset-id': x.cover }, display: { result: true } })]
  return []
}

export function createDemoApi(img: Img) {
  const jobs = sampleJobs()
  const projects = [
    { biz_id: 'P1', name: '海岸之城', description: '在阳光明媚的海滨城市，感受海风与山坡小镇的宁静。', created_at: T('01T00:00'), asset_count: 24, job_count: 8, character_count: 2, last_activity_at: T('24T06:20'), cover_urls: [img('tram-hero.jpg')] },
    { biz_id: 'P2', name: '日落计划', description: '', created_at: T('05T00:00'), asset_count: 3, job_count: 2, character_count: 0, last_activity_at: T('20T08:00'), cover_urls: [img('sunset.jpg')] },
  ]
  const characters = [
    { biz_id: 'C1', name: '小悠', description: '年轻的亚洲女性，黑色长发，温柔的笑容', ref_asset_ids: ['portrait'], seed: 362418, project_id: 'P1' },
    { biz_id: 'C2', name: '阿橘', description: '一只橘色的猫，喜欢晒太阳', ref_asset_ids: ['cat', 'sunset'], seed: 1024, project_id: 'P1' },
  ]
  const presets = [
    { biz_id: 'S1', category: 'style', name: '水彩', name_en: 'Watercolor', cover_url: '/preset-covers/style-watercolor.jpg', prompt_fragment: '水彩质感，手绘笔触，柔和的色彩过渡', priority: 1, style_type: 'watercolor', mine: false },
    { biz_id: 'S2', category: 'style', name: '漫画', name_en: 'Manga', cover_url: '/preset-covers/style-manga.jpg', prompt_fragment: '日系漫画风格，干净的线条', priority: 2, style_type: 'manga', mine: false },
    { biz_id: 'S3', category: 'lighting', name: '黄金时刻', name_en: 'Golden hour', cover_url: '/preset-covers/lighting-golden.jpg', prompt_fragment: '黄金时刻的暖色光线', priority: 3, style_type: '', mine: false },
    { biz_id: 'S4', category: 'camera', name: '特写', name_en: 'Close-up', cover_url: '/preset-covers/camera-closeup.jpg', prompt_fragment: '特写镜头，浅景深', priority: 4, style_type: '', mine: false },
  ]
  const assetBody = (id: string) => {
    const video = CLIPS.includes(id)
    return {
      biz_id: id, type: video ? 'video' : 'image', public_url: img(`${id}.${video ? 'mp4' : 'jpg'}`), thumb_url: video ? '' : img(`${id}.jpg`),
      mime: video ? 'video/mp4' : 'image/jpeg', duration_ms: video ? 2000 : 0, width: video ? 640 : 1536, height: video ? 360 : 1024,
      resolution_tag: video ? '768P' : '', created_at: T('24T06:20'), project_id: 'P1', is_public: false, prompt: '',
    }
  }
  let nextId = 100
  const estimateOf = (r: { workflow_name?: string; spec?: Record<string, unknown> }) => {
    const spec = r.spec ?? {}
    const n = typeof spec.n === 'number' ? spec.n : 1
    if (r.workflow_name === 'image.comic4') return { credits_total: 72, items: [{ kind: 'comic4_panels', count: 1, credits: 72, basis: 'reservation' }] }
    if (spec.image_provider === 'openai') {
      const per = spec.image_quality === 'low' ? 9 : spec.image_quality === 'medium' ? 18 : 36
      return { credits_total: n * per, items: [{ kind: 'image_single', count: n, credits: n * per, basis: 'reservation' }] }
    }
    return { credits_total: n * 10, items: [{ kind: 'image_single', count: n, credits: n * 10 }] }
  }
  const me = { biz_id: 'demo-user', email: 'demo@example.com', phone: null, balance: 860, held: 350, is_admin: true, entitlements: ['openai_image'], comic_ai: true }

  const view = (x: Job) => ({
    biz_id: x.biz_id,
    workflow_name: x.workflow_name,
    title: x.title,
    status: x.status,
    node_total: x.node_total,
    node_done: x.node_done,
    node_failed: x.node_failed,
    credit_estimated: x.reserved,
    credit_held: x.reserved,
    credit_settled: x.settled,
    credits: {
      reserved: x.reserved,
      settled: x.settled,
      overage: 0,
      released: ['succeeded', 'partial', 'failed', 'cancelled'].includes(x.status) ? Math.max(0, x.reserved - x.settled) : 0,
      frozen: ['succeeded', 'partial', 'failed', 'cancelled'].includes(x.status) ? 0 : x.reserved - x.settled,
    },
    error_code: x.error_code,
    error_msg: '',
    created_at: x.created_at,
    started_at: x.created_at,
    finished_at: null,
    retry_of_job_id: '',
    project_id: x.project_id,
    cover_asset_id: x.cover,
    cover_url: img(`${x.cover}.jpg`),
    cover_type: x.workflow_name.startsWith('video') ? 'video' : 'image',
  })

  const list = (q: URLSearchParams) => {
    let rows = [...jobs]
    const bucket = q.get('bucket')
    const status = q.get('status')
    if (bucket && !BUCKET[bucket]) return { status: 400, body: { code: 'bad_request', message: 'unknown bucket' } }
    if (bucket) rows = rows.filter((x) => BUCKET[bucket].includes(x.status))
    if (status) rows = rows.filter((x) => x.status === status)
    const exclude = q.get('exclude_status')?.split(',') ?? []
    rows = rows.filter((x) => !exclude.includes(x.status))
    if (q.get('workflow')) rows = rows.filter((x) => x.workflow_name === q.get('workflow'))
    if (q.get('project_id')) rows = rows.filter((x) => x.project_id === q.get('project_id'))
    if (q.get('q')) rows = rows.filter((x) => x.title.includes(q.get('q')!))
    rows.sort((a, b) => (q.get('order') === 'oldest' ? a.id - b.id : b.id - a.id))
    const cursor = Number(q.get('cursor') ?? 0)
    if (cursor) rows = rows.filter((x) => (q.get('order') === 'oldest' ? x.id > cursor : x.id < cursor))
    const limit = Number(q.get('limit') ?? 20)
    const page = rows.slice(0, limit)
    return { status: 200, body: { jobs: page.map(view), ...(page.length === limit && rows.length > limit ? { next_cursor: String(page[page.length - 1].id) } : {}) } }
  }

  const summary = (q: URLSearchParams) => {
    const rows = q.get('project_id') ? jobs.filter((x) => x.project_id === q.get('project_id')) : jobs
    const statuses: Record<string, number> = Object.fromEntries(STATUSES.map((s) => [s, 0]))
    rows.forEach((x) => statuses[x.status]++)
    const out: Record<string, unknown> = { statuses, total: rows.length }
    for (const [b, sts] of Object.entries(BUCKET)) out[b] = sts.reduce((n, s) => n + statuses[s], 0)
    return out
  }

  return function handle(method: string, path: string, search: string, body: unknown): DemoResponse {
    const q = new URLSearchParams(search)
    const find = (id: string) => jobs.find((x) => x.biz_id === id)
    if (path === '/stream') return { status: 200, hang: true }
    if (path === '/me') return { status: 200, body: me }
    if (path === '/projects') return { status: 200, body: { projects } }
    if (path === '/jobs/summary') return { status: 200, body: summary(q) }
    if (path === '/jobs' && method === 'GET') {
      jobs.forEach(liveStatus)
      return list(q)
    }
    if (path === '/characters' && method === 'GET') return { status: 200, body: { characters } }
    if (path === '/presets' && method === 'GET') return { status: 200, body: { presets } }
    if (path === '/assets' && method === 'GET') return { status: 200, body: { assets: IMAGES.filter((i) => i !== 'tram-hero').map(assetBody) } }
    const r = (body ?? {}) as { workflow_name?: string; spec?: Record<string, unknown>; quote_total?: number; project_id?: string }
    if (path === '/jobs/estimate' && method === 'POST') {
      if (!String(r.spec?.text ?? '').trim()) return { status: 422, body: { code: 'text_length', message: 'empty', params: { field: 'text', max: 1500 } } }
      return { status: 200, body: estimateOf(r) }
    }
    if (path === '/jobs/preview' && method === 'POST') {
      const fragments = ((r.spec?.preset_ids as string[] | undefined) ?? []).map((id) => presets.find((p) => p.biz_id === id)?.prompt_fragment).filter(Boolean)
      const refs = r.spec?.source_image_asset_id ? [r.spec.source_image_asset_id as string] : (r.spec?.reference_image_asset_ids as string[] | undefined) ?? []
      return { status: 200, body: { prompt: [r.spec?.text, ...fragments].join('，'), references: refs } }
    }
    if (path === '/jobs' && method === 'POST') {
      const est = estimateOf(r)
      if (r.quote_total !== undefined && r.quote_total !== est.credits_total) {
        return { status: 409, body: { code: 'price_changed', message: 'changed', params: { credits_total: est.credits_total } } }
      }
      const n = typeof r.spec?.n === 'number' ? (r.spec.n as number) : 1
      const id = nextId++
      const text = String(r.spec?.text ?? '')
      jobs.unshift({
        biz_id: `N${id}`, id: 1000 + id, workflow_name: r.workflow_name ?? 'image.single', title: text.slice(0, 20), status: 'running',
        node_total: 1, node_done: 0, node_failed: 0, reserved: est.credits_total, settled: 0, error_code: '', created_at: new Date().toISOString(),
        project_id: r.project_id ?? '', cover: 'tram-1', createdMs: Date.now(), assets: ['tram-1', 'tram-2', 'tram-3', 'tram-4', 'bay', 'sunset', 'cat', 'lemon', 'night'].slice(0, n), spec: r.spec,
      })
      return { status: 200, body: { biz_id: `N${id}`, status: 'running', workflow_run_id: '' } }
    }
    if (path === '/prompts/rewrite' && method === 'POST') {
      return { status: 200, body: { text: `${String((body as { text?: string })?.text ?? '').replace(/[。.]$/, '')}，清晨柔和的光线，电影感构图。` } }
    }
    if (path === '/trial/image' && method === 'POST') return { status: 200, body: { image_url: img('tram-hero.jpg') } }
    const m = path.match(/^\/jobs\/([^/]+)(\/cancel)?$/)
    if (m) {
      const x = find(m[1])
      if (!x) return { status: 404, body: { code: 'not_found', message: 'job not found' } }
      if (m[2] && method === 'POST') {
        if (!['queued', 'running', 'awaiting_review'].includes(x.status)) return { status: 409, body: { code: 'job_finished', message: 'finished' } }
        x.status = 'cancelling'
        return { status: 204 }
      }
      if (method === 'PATCH') {
        x.title = String((body as { title?: string })?.title ?? x.title)
        return { status: 204 }
      }
      if (method === 'DELETE') {
        if (['queued', 'running', 'awaiting_review', 'cancelling'].includes(x.status)) {
          return { status: 409, body: { code: 'job_active', message: 'running', params: { status: x.status } } }
        }
        jobs.splice(jobs.indexOf(x), 1)
        return { status: 204 }
      }
      if (method === 'GET') {
        liveStatus(x)
        return {
          status: 200,
          body: { ...view(x), nodes: nodesFor(x), spec: x.spec ?? SPECS[x.biz_id] ?? {}, review_deadline: x.status === 'awaiting_review' ? '2026-09-30T06:10:00Z' : null },
        }
      }
    }
    if (path === '/capabilities') {
      return {
        status: 200,
        body: {
          image: { max_n: 9, max_prompt_chars: 1500 },
          video: { duration_min: 4, duration_max: 15, max_prompt_chars: 7000, resolutions: ['768P', '2K'], ratios: ['21:9', '16:9', '4:3', '1:1', '3:4', '9:16'] },
          comic: { openai_enabled: true, model: 'gpt-image-2.5-flare', max_composed_chars: 20000, max_references: 15 },
          providers: {
            minimax: { enabled: true },
            openai: {
              enabled: true, image_model: 'gpt-image-2.5-flare', entitlement: 'openai_image', sizes: ['1024x1024', '1536x1024', '1024x1536'],
              qualities: ['low', 'medium', 'high'], max_n: 4, default_quality: 'high', max_references: 16,
              reference_formats: ['image/png', 'image/jpeg', 'image/webp'], max_reference_bytes: 20 << 20,
            },
            gemini: { enabled: false, image_model: '' },
          },
        },
      }
    }
    const review = path.match(/^\/jobs\/([^/]+)\/resume(\/quote)?$/)
    if (review && method === 'POST') {
      const x = find(review[1])
      if (!x || x.status !== 'awaiting_review') return { status: 409, body: { code: 'not_awaiting_review', message: 'not waiting' } }
      const d = (body ?? {}) as { selected_shots?: number[]; redo_shots?: number[]; quote_total?: number }
      const redo = d.redo_shots?.length ?? 0
      const up = d.selected_shots?.length ?? 0
      const items = [
        ...(redo ? [{ kind: 'video_redo', count: redo, credits: redo * 40 }] : []),
        ...(up ? [{ kind: 'video_upgrade', count: up, credits: up * 64 }] : []),
        { kind: 'video_compose', count: 1, credits: 0 },
      ]
      const total = redo * 40 + up * 64
      if (review[2]) return { status: 200, body: { items, total, all_upgrade: 3 * 64 } }
      if (d.quote_total !== total) return { status: 409, body: { code: 'price_changed', message: 'changed', params: { credits_total: total } } }
      x.status = 'running'
      x.reserved += total
      return { status: 204 }
    }
    const asset = path.match(/^\/assets\/([^/]+)$/)
    if (asset && method === 'GET') {
      const id = asset[1]
      if (!CLIPS.includes(id) && !IMAGES.includes(id)) return { status: 404, body: { code: 'not_found', message: 'asset' } }
      return { status: 200, body: assetBody(id) }
    }
    if (asset && method === 'PATCH') return { status: 204 }
    const lists: Record<string, unknown> = {
      '/community/feed': { assets: [] },
      '/credits/ledger': { entries: [] },
    }
    if (method === 'GET' && path in lists) return { status: 200, body: lists[path] }
    return { status: 404, body: { code: 'not_found', message: `demo has no ${method} ${path}` } }
  }
}
