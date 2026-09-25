import { test, expect, type Page } from '@playwright/test'
import { useDemoApi } from './demo'
import { createDemoApi } from '../../src/demo/api'

function captureJobs(page: Page) {
  const bodies: Record<string, unknown>[] = []
  page.on('request', (r) => {
    if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/jobs') bodies.push(r.postDataJSON())
  })
  return bodies
}

const panel = (page: Page, n: number) => page.getByRole('textbox', { name: `第 ${n} 格`, exact: true })

test('written panels are sent as they are, and every panel must be filled', async ({ page }) => {
  await useDemoApi(page)
  const jobs = captureJobs(page)
  await page.goto('/create/comic-classic')
  await page.getByRole('radiogroup', { name: '分镜数量' }).getByRole('radio', { name: '3' }).click()
  await panel(page, 1).fill('柠檬挂在枝头')
  await panel(page, 2).fill('女孩摘下柠檬')
  await expect(page.getByText('请填写每一格的内容')).toBeVisible()
  await expect(page.getByRole('button', { name: '生成漫画（3 格）' })).toBeDisabled()
  await panel(page, 3).fill('举杯干杯')
  await expect(page.getByText('36 积分', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '生成漫画（3 格）' }).click()
  await expect.poll(() => jobs.length).toBe(1)
  expect(jobs[0]).toMatchObject({ workflow_name: 'image.comic4', spec: { panels: ['柠檬挂在枝头', '女孩摘下柠檬', '举杯干杯'] } })
  expect((jobs[0].spec as Record<string, unknown>).image_provider).toBeUndefined()
  await expect(page.getByText('对白和文字会直接画进图片，生成后不能再编辑。')).toBeVisible()
})

test('a story is sent with the panel count to split into', async ({ page }) => {
  await useDemoApi(page)
  const jobs = captureJobs(page)
  await page.goto('/create/comic-classic')
  await page.getByRole('tab', { name: '从故事拆分' }).click()
  await page.getByRole('radiogroup', { name: '分镜数量' }).getByRole('radio', { name: '5' }).click()
  await page.getByRole('textbox', { name: '输入你的故事' }).fill('一个夏天的午后，女孩在院子里做柠檬汽水，和朋友分享。')
  await page.getByRole('button', { name: '生成漫画（5 格）' }).click()
  await expect.poll(() => jobs.length).toBe(1)
  expect(jobs[0].spec).toEqual({ story: '一个夏天的午后，女孩在院子里做柠檬汽水，和朋友分享。', n: 5 })
})

test('Gemini is offered only where it is configured', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/create/comic-classic')
  await expect(page.getByRole('radiogroup', { name: '生成模型' }).getByRole('radio', { name: 'Gemini' })).toBeDisabled()
  await expect(page.getByText('Gemini 暂不可用：此环境没有配置')).toBeVisible()

  const caps = createDemoApi((f) => f)('GET', '/capabilities', '', undefined).body as { providers: { gemini: { enabled: boolean } } }
  caps.providers.gemini.enabled = true
  await page.route('**/api/v1/capabilities', (route) => route.fulfill({ json: caps }))
  const jobs = captureJobs(page)
  await page.reload()
  await page.getByRole('radiogroup', { name: '生成模型' }).getByRole('radio', { name: 'Gemini' }).click()
  for (let i = 1; i <= 4; i++) await panel(page, i).fill(`第 ${i} 格的画面`)
  await page.getByRole('button', { name: '生成漫画（4 格）' }).click()
  await expect.poll(() => jobs.length).toBe(1)
  expect(jobs[0].spec).toMatchObject({ image_provider: 'gemini' })
})

test('the classic comic page is fully translated in English', async ({ page }) => {
  await useDemoApi(page, { lang: 'en' })
  await page.goto('/create/comic-classic')
  await expect(page.getByRole('heading', { name: 'Classic comic', level: 1 })).toBeVisible()
  for (const tab of ['Split a story', 'Write panels']) {
    await page.getByRole('tab', { name: tab }).click()
    const untranslated = await page.evaluate(() => {
      const main = document.querySelector('main')!.cloneNode(true) as HTMLElement
      main.querySelectorAll('select, option, textarea, input').forEach((el) => el.remove())
      return [...main.querySelectorAll('button, label, h1, h2, h3, legend, p, span')]
        .map((el) => el.textContent ?? '')
        .filter((text) => /[一-鿿]/.test(text))
    })
    expect(untranslated).toEqual([])
  }
})
