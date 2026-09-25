import { test, expect, type Page } from '@playwright/test'
import { readFile } from 'node:fs/promises'
import { newComic } from '../../src/lib/comicDocument'

async function setup(page: Page, { comicAI = true, lang = 'zh' } = {}) {
  const document = newComic(); document.title = '港燈 ESG 漫画'; document.brief = '清新扁平插画，阿健是工程师，小智是机器人。四格故事。'; document.page_asset_id = 'page-original'
  let saved = { biz_id: 'draft-one', version: 1, document }
  const state = { conflict: false, finished: false, jobRequests: [] as Record<string, unknown>[], assetUploads: 0 }
  await page.addInitScript((l) => {
    localStorage.setItem('aigc.auth', JSON.stringify({ accessToken: 'test-token', refreshToken: 'test-refresh' }))
    localStorage.setItem('aigc.lang', l)
  }, lang)
  await page.route('**/api/v1/**', async route => {
    const req = route.request(), path = new URL(req.url()).pathname.split('/api/v1')[1]
    const send = (data: unknown, status = 200) => route.fulfill({ json: data, status })
    if (path === '/me') return send({ biz_id: 'test-user', email: 'test@example.com', phone: null, balance: 1000, held: 0, is_admin: false, comic_ai: comicAI, entitlements: comicAI ? ['openai_image'] : [] })
    if (path === '/projects') return send({ projects: [] })
    if (path === '/jobs/summary') return send({ needs_review: 0, active: 0, succeeded: 0, failed: 0, cancelled: 0, total: 0, statuses: {} })
    if (path === '/stream') return route.fulfill({ status: 200, contentType: 'text/event-stream', body: ': heartbeat\n\n' })
    if (path === '/capabilities') return send({ image: { max_n: 9, max_prompt_chars: 1500 }, comic: { openai_enabled: true, model: 'gpt-image-2.5-flare' }, video: { resolutions: [], ratios: [] } })
    if (path === '/comics' && req.method() === 'GET') return send({ comics: [{ biz_id: saved.biz_id, title: saved.document.title, version: saved.version }] })
    if (path.startsWith('/comics') && req.method() !== 'GET') {
      if (state.conflict) return send({ code: 'version_conflict', message: '编辑稿已在其他窗口更新，请重新载入或保存副本。' }, 409)
      const body = req.postDataJSON(); saved = { ...saved, version: saved.version + 1, document: body.document }; return send(saved)
    }
    if (path.startsWith('/comics/')) return send(saved)
    if (path === '/jobs/estimate') return send({ credits_total: 53, items: [{ kind: 'comic4_panels', count: 1, credits: 53 }] })
    if (path === '/jobs' && req.method() === 'POST') { state.jobRequests.push(req.postDataJSON()); return send({ biz_id: 'generation-one', status: 'running', workflow_run_id: 'run-one' }) }
    if (path.startsWith('/jobs/')) return send({
      biz_id: 'generation-one', title: '四格漫画', spec: { text: document.brief, comic_mode: 'editable', image_provider: 'openai' },
      status: state.finished ? 'succeeded' : 'running', workflow_name: 'image.comic4', retry_of_job_id: '', project_id: '', error_code: '', cover_asset_id: '',
      created_at: '2026-09-24T06:00:00Z', started_at: '2026-09-24T06:00:00Z', finished_at: state.finished ? '2026-09-24T06:01:00Z' : null, review_deadline: null,
      credits: { reserved: 53, settled: state.finished ? 40 : 0, overage: 0, released: state.finished ? 13 : 0, frozen: state.finished ? 0 : 53 },
      nodes: state.finished ? [{ name: 'compose', status: 'succeeded', outputs: { 'asset-id': 'new-image', 'usage-known': true }, display: { result: true }, attempt: 1, error: '', error_code: '', queue_reason: '', started_at: null, finished_at: null }] : [],
    })
    if (path === '/assets' && req.method() === 'GET') return send({ assets: [
      { biz_id: 'lib-png', type: 'image', mime: 'image/png', public_url: 'http://127.0.0.1:4173/preset-covers/style-manga.jpg' },
      { biz_id: 'lib-webp', type: 'image', mime: 'image/webp', public_url: 'http://127.0.0.1:4173/preset-covers/style-manga.jpg' },
      { biz_id: 'lib-gif', type: 'image', mime: 'image/gif', public_url: 'http://127.0.0.1:4173/preset-covers/style-manga.jpg' },
    ] })
    if (path === '/assets/upload-url') { state.assetUploads++; return send({ biz_id: `upload-${state.assetUploads}`, upload_url: 'http://127.0.0.1:4173/mock-upload', storage_key: 'test.png' }) }
    if (path.startsWith('/assets/')) return send({ biz_id: path.split('/')[2], public_url: 'http://127.0.0.1:4173/preset-covers/style-manga.jpg', mime: 'image/jpeg', type: 'image', width: 1536, height: 1024 })
    if (path === '/jobs') return send({ jobs: [] })
    if (path === '/credits/balance') return send({ balance: 1000, held: 0 })
    return send({})
  })
  await page.route('**/mock-upload', route => route.fulfill({ status: 200 }))
  await page.goto('/comics')
  if (lang === 'zh') {
    await page.getByLabel('已保存的编辑稿').selectOption('draft-one')
    await expect(page.getByLabel('标题', { exact: true })).toHaveValue('港燈 ESG 漫画')
  }
  return state
}

test('Chinese text, drag, resize, lock, undo and persisted reload', async ({ page }) => {
  await setup(page)
  await page.getByRole('button', { name: '添加对话框', exact: true }).click()
  const field = page.getByLabel('对白文字', { exact: true })
  await field.fill('港燈與小智，一起照亮香港！\n99.9999% 𠮷')
  await expect(field).toHaveValue('港燈與小智，一起照亮香港！\n99.9999% 𠮷')
  const layer = page.locator('.comic-layer').first(), before = await layer.getAttribute('style')
  const bounds = (await layer.boundingBox())!
  await page.mouse.move(bounds.x + 30, bounds.y + 30); await page.mouse.down(); await page.mouse.move(bounds.x + 80, bounds.y + 65); await page.mouse.up()
  await expect(layer).not.toHaveAttribute('style', before!)
  const resize = (await page.locator('.comic-resize').boundingBox())!
  await page.mouse.move(resize.x + 10, resize.y + 10); await page.mouse.down(); await page.mouse.move(resize.x + 40, resize.y + 30); await page.mouse.up()
  await page.getByRole('button', { name: '锁定', exact: true }).click(); await expect(field).toBeDisabled()
  await page.getByRole('button', { name: '解锁', exact: true }).click()
  await page.getByRole('button', { name: '复制', exact: true }).click(); await expect(page.locator('.comic-layer')).toHaveCount(2)
  await page.getByRole('button', { name: '撤销', exact: true }).click(); await expect(page.locator('.comic-layer')).toHaveCount(1)
  await page.getByRole('button', { name: '保存编辑稿', exact: true }).click(); await expect(page.getByRole('main').getByRole('status')).toContainText('已保存')
  await page.screenshot({ path: 'test-results/comic-editor-desktop.png', fullPage: true })
  page.on('dialog', dialog => dialog.accept())
  await page.reload()
  await expect(page.getByRole('button', { name: '恢复本机编辑' })).toBeVisible()
  await page.getByRole('button', { name: '恢复本机编辑' }).click()
  await expect(page.locator('.comic-layer')).toHaveCount(1)
  await page.locator('.comic-layer-list button').first().click()
  await expect(field).toHaveValue('港燈與小智，一起照亮香港！\n99.9999% 𠮷')
})

test('exports a real PNG and blocks clipped dialogue', async ({ page }) => {
  await setup(page)
  await page.getByRole('button', { name: '添加对话框', exact: true }).click()
  await page.getByLabel('对白文字', { exact: true }).fill('绿色香港')
  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出 PNG', exact: true }).click()
  const download = await downloadPromise, data = await readFile((await download.path())!)
  expect(data.subarray(1, 4).toString()).toBe('PNG'); expect(data.readUInt32BE(16)).toBe(1536); expect(data.readUInt32BE(20)).toBe(1024)
  await page.getByLabel('对白文字', { exact: true }).fill('很长的对白'.repeat(100))
  await expect(page.getByRole('alert')).toContainText('溢出')
  await page.getByRole('button', { name: '导出 PNG', exact: true }).click()
  await expect(page.getByText('对白超出对话框，请放大对话框或减小字号后再导出。')).toBeVisible()
})

test('200k source stays background; only reviewed excerpts go to generation', async ({ page }) => {
  const state = await setup(page)
  await page.getByText('小册子背景资料', { exact: true }).click()
  const source = '普通资料。'.repeat(39800) + '社区绿色能源目标，不得编造尚未建设的风电场。'
  await page.getByLabel('导入背景资料', { exact: true }).setInputFiles({ name: 'booklet.txt', mimeType: 'text/plain', buffer: Buffer.from(source) })
  await expect.poll(async () => (await page.getByLabel('背景原文', { exact: true }).inputValue()).length).toBe(source.length)
  await page.getByRole('button', { name: '按故事提取相关摘录' }).click()
  const excerpt = await page.getByLabel('送给模型的背景摘录（请核对事实）').inputValue()
  expect(excerpt.length).toBeLessThan(8000)
  // Excerpts are sent only after the user confirms checking them (spec D25).
  await page.getByRole('button', { name: '检查费用并生成' }).click()
  await expect(page.getByRole('alert')).toContainText('请先确认已检查将发送的摘录')
  await page.getByLabel('我已检查将发送的摘录').check()
  await page.getByRole('button', { name: '检查费用并生成' }).click()
  await page.getByRole('button', { name: '确认生成整页' }).click()
  await expect.poll(() => state.jobRequests.length).toBe(1)
  const spec = state.jobRequests[0].spec as Record<string, unknown>
  expect(spec.comic_context).toBe(excerpt); expect(spec.background).toBeUndefined(); expect(spec.image_provider).toBe('openai')
})

test('single panel completion preserves edits made while generating', async ({ page }) => {
  const state = await setup(page)
  await page.getByRole('button', { name: '添加对话框', exact: true }).click()
  await page.getByLabel('对白文字', { exact: true }).fill('旧对白')
  await page.getByLabel('本次生成目标').selectOption('2')
  await page.getByRole('button', { name: '检查费用并生成' }).click()
  await page.getByRole('button', { name: '确认生成第 2 格' }).click()
  await expect.poll(() => state.jobRequests.length).toBe(1)
  await page.getByLabel('对白文字', { exact: true }).fill('生成中修改的对白不能丢失')
  state.finished = true
  await expect(page.getByText('生成完成。对白和 Logo 图层已保留。请保存编辑稿。')).toBeVisible({ timeout: 12000 })
  await expect(page.getByLabel('对白文字', { exact: true })).toHaveValue('生成中修改的对白不能丢失')
  const spec = state.jobRequests[0].spec as Record<string, unknown>
  expect(spec.comic_panel).toBe(2); expect(spec.source_image_asset_id).toBe('page-original')
  await page.getByRole('button', { name: '保存编辑稿', exact: true }).click()
})

test('saving conflict leaves current text intact', async ({ page }) => {
  const state = await setup(page); state.conflict = true
  await page.getByRole('button', { name: '添加对话框', exact: true }).click()
  await page.getByLabel('对白文字', { exact: true }).fill('必须保留的修改')
  await page.getByRole('button', { name: '保存编辑稿', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('别处修改')
  await expect(page.getByLabel('对白文字', { exact: true })).toHaveValue('必须保留的修改')
})

test('native composition events retain Chinese text without generation calls', async ({ page }) => {
  const state = await setup(page)
  await page.getByRole('button', { name: '添加对话框', exact: true }).click()
  const field = page.getByLabel('对白文字', { exact: true })
  await field.dispatchEvent('compositionstart', { data: '' })
  await field.fill('港燈關懷社區')
  await field.dispatchEvent('compositionupdate', { data: '港燈關懷社區' })
  await field.dispatchEvent('keydown', { key: 'Enter', isComposing: true })
  await field.dispatchEvent('compositionend', { data: '港燈關懷社區' })
  await expect(field).toHaveValue('港燈關懷社區'); expect(state.jobRequests).toHaveLength(0)
})

function pdfFixture(text: string): Buffer {
  const stream = text ? `BT /F1 12 Tf 40 760 Td (${text}) Tj ET` : ''
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>', '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 800] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>',
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>', `<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`,
  ]
  let pdf = '%PDF-1.4\n'; const offsets = [0]
  objects.forEach((obj, i) => { offsets.push(Buffer.byteLength(pdf)); pdf += `${i + 1} 0 obj\n${obj}\nendobj\n` })
  const xref = Buffer.byteLength(pdf)
  pdf += `xref\n0 6\n0000000000 65535 f \n${offsets.slice(1).map(n => `${String(n).padStart(10, '0')} 00000 n \n`).join('')}trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF`
  return Buffer.from(pdf)
}
test('PDF text import retains page provenance; empty PDF requests OCR', async ({ page }) => {
  await setup(page); await page.getByText('小册子背景资料', { exact: true }).click()
  await page.getByLabel('导入背景资料', { exact: true }).setInputFiles({ name: 'booklet.pdf', mimeType: 'application/pdf', buffer: pdfFixture('Green energy and community care. Verified source facts.') })
  await expect(page.getByLabel('背景原文', { exact: true })).toHaveValue(/第 1 页.*Green energy/s)
  await page.getByLabel('导入背景资料', { exact: true }).setInputFiles({ name: 'scanned.pdf', mimeType: 'application/pdf', buffer: pdfFixture('') })
  await expect(page.getByRole('alert')).toContainText('OCR')
  await expect(page.getByLabel('背景原文', { exact: true })).toHaveValue(/Green energy/)
})
test('logo uploads as an independent layer and is not sent as a character reference', async ({ page }) => {
  const state = await setup(page)
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', 'base64')
  await page.getByLabel('添加 Logo', { exact: true }).setInputFiles({ name: 'brand.png', mimeType: 'image/png', buffer: png })
  await expect(page.locator('.comic-layer-list')).toContainText('Logo')
  expect(state.assetUploads).toBe(1)
  await page.getByRole('button', { name: '检查费用并生成' }).click(); await page.getByRole('button', { name: '确认生成整页' }).click()
  await expect.poll(() => state.jobRequests.length).toBe(1)
  expect((state.jobRequests[0].spec as Record<string, unknown>).reference_image_asset_ids).toEqual([])
})

test('regenerate from a new comic job opens the new editor with its brief', async ({ page }) => {
  const state = await setup(page); state.finished = true
  page.on('dialog', dialog => dialog.accept())
  await page.goto('/jobs/generation-one')
  await page.getByRole('button', { name: '以此再生成', exact: true }).click()
  await expect(page).toHaveURL(/\/create\/comic$/)
  await expect(page.getByLabel('故事、画风与四格画面要求')).toHaveValue('清新扁平插画，阿健是工程师，小智是机器人。四格故事。')
  expect(state.jobRequests).toHaveLength(0)
})

for (const width of [375, 768, 1440]) test(`responsive ${width}px, light theme and no horizontal overflow`, async ({ page }) => {
  await page.setViewportSize({ width, height: 900 }); await setup(page)
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await expect(page.getByRole('button', { name: '添加对话框', exact: true })).toBeVisible()
  await page.screenshot({ path: `test-results/comic-${width}.png`, fullPage: true })
})

test('accounts outside the OpenAI beta can edit but not generate', async ({ page }) => {
  const state = await setup(page, { comicAI: false })
  await expect(page.getByText('AI 生成目前为内测功能')).toBeVisible()
  await expect(page.getByRole('button', { name: '检查费用并生成' })).toBeDisabled()
  await page.getByRole('button', { name: '添加对话框', exact: true }).click()
  await expect(page.locator('.comic-layer')).toHaveCount(1)
  expect(state.jobRequests).toHaveLength(0)
})

test('references come from the asset library, WebP allowed, GIF filtered out', async ({ page }) => {
  const state = await setup(page)
  await expect(page.getByRole('heading', { name: '人物／画风参考（0 / 15）' })).toBeVisible()
  await page.getByRole('button', { name: '+ 选择' }).click()
  const grid = page.locator('.comic-inputs .overflow-y-auto button')
  await expect(grid).toHaveCount(2) // png + webp; the gif is not offered
  await grid.nth(0).click(); await grid.nth(1).click()
  await expect(page.getByRole('heading', { name: '人物／画风参考（2 / 15）' })).toBeVisible()
  await page.getByLabel('参考图 1 的用途').fill('阿健的长相')
  await page.getByRole('button', { name: '检查费用并生成' }).click(); await page.getByRole('button', { name: '确认生成整页' }).click()
  await expect.poll(() => state.jobRequests.length).toBe(1)
  const spec = state.jobRequests[0].spec as Record<string, unknown>
  expect(spec.reference_image_asset_ids).toEqual(['lib-png', 'lib-webp'])
  expect(spec.text).toContain('参考图 1：阿健的长相')
})

test('the comic editor is fully translated in English', async ({ page }) => {
  await setup(page, { lang: 'en' })
  await expect(page.getByRole('heading', { name: 'Four-panel comic studio' })).toBeVisible()
  await page.getByLabel('Saved drafts').selectOption('draft-one')
  await expect(page.getByLabel('Title', { exact: true })).toHaveValue('港燈 ESG 漫画')
  await page.getByRole('button', { name: 'Add speech bubble' }).click()
  await page.getByText('Background material', { exact: true }).click()
  const untranslated = await page.evaluate(() => {
    const main = document.querySelector('main')!.cloneNode(true) as HTMLElement
    main.querySelectorAll('select, option, textarea, input').forEach((el) => el.remove())
    return [...main.querySelectorAll('button, label, h1, h2, legend, summary, p')]
      .map((el) => el.textContent ?? '')
      .filter((text) => /[\u4e00-\u9fff]/.test(text))
  })
  expect(untranslated).toEqual([])
})
