// A fake API with sample data matching the design boards, for screenshot
// acceptance (demo mode, VITE_DEMO=1) and Playwright tests. Pure: images
// are resolved by the caller; state lives in the returned instance.

export interface DemoResponse {
  status: number
  body?: unknown
  /** Keep the connection open with no data (the event stream). */
  hang?: boolean
}

type Img = (name: string) => string

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
    j(12, 'video.sequence', '海岸电车之旅 · 宣传视频', 'awaiting_review', 2, 4, 120, 64, '24T06:20', 'tram-1'),
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

export function createDemoApi(img: Img) {
  const jobs = sampleJobs()
  const projects = [
    { biz_id: 'P1', name: '海岸之城', description: '在阳光明媚的海滨城市，感受海风与山坡小镇的宁静。', created_at: T('01T00:00'), asset_count: 24, job_count: 8, character_count: 2, last_activity_at: T('24T06:20'), cover_urls: [img('tram-hero')] },
    { biz_id: 'P2', name: '日落计划', description: '', created_at: T('05T00:00'), asset_count: 3, job_count: 2, character_count: 0, last_activity_at: T('20T08:00'), cover_urls: [img('sunset')] },
  ]
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
    cover_url: img(x.cover),
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
    if (path === '/jobs' && method === 'GET') return list(q)
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
      if (method === 'GET') return { status: 200, body: { ...view(x), nodes: [], spec: {}, review_deadline: null } }
    }
    if (path === '/capabilities') {
      return { status: 200, body: { image: { max_n: 9, max_prompt_chars: 1500 }, video: { duration_min: 4, duration_max: 15, max_prompt_chars: 7000, resolutions: ['768P', '2K'], ratios: ['21:9', '16:9', '4:3', '1:1', '3:4', '9:16'] } } }
    }
    const lists: Record<string, unknown> = {
      '/assets': { assets: [] },
      '/characters': { characters: [] },
      '/presets': { presets: [] },
      '/community/feed': { assets: [] },
      '/credits/ledger': { entries: [] },
    }
    if (method === 'GET' && path in lists) return { status: 200, body: lists[path] }
    return { status: 404, body: { code: 'not_found', message: `demo has no ${method} ${path}` } }
  }
}
