import { test, expect, type Page } from '@playwright/test'
import { useDemoApi } from './demo'

function captureJobs(page: Page) {
  const bodies: Record<string, unknown>[] = []
  page.on('request', (r) => {
    if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/jobs') bodies.push(r.postDataJSON())
  })
  return bodies
}

const shot = (page: Page, n: number) => page.getByRole('textbox', { name: `镜头 ${n}`, exact: true })

async function fill(page: Page, texts: string[]) {
  for (let i = 0; i < texts.length; i++) await shot(page, i + 1).fill(texts[i])
}

test('previews come first by default; choosing 2K renames the action and both choices are priced', async ({ page }) => {
  await useDemoApi(page)
  const jobs = captureJobs(page)
  await page.goto('/create/video-sequence')
  await fill(page, ['海边露台', '石板街道', '海边咖啡馆'])
  await expect(page.getByRole('radio', { name: /先预览再确认/ })).toBeChecked()
  await expect(page.getByRole('button', { name: '生成预览（3 个镜头）' })).toBeVisible()
  await expect(page.getByText('预计 75 积分')).toBeVisible()
  await expect(page.getByText('预计 180 积分')).toBeVisible()
  await page.getByText('直接生成 2K', { exact: true }).click()
  await expect(page.getByRole('button', { name: '直接生成 2K（3 个镜头）' })).toBeEnabled()
  await expect(page.getByRole('button', { name: /生成预览/ })).toHaveCount(0)
  await page.getByRole('button', { name: '直接生成 2K（3 个镜头）' }).click()
  await expect.poll(() => jobs.length).toBe(1)
  expect(jobs[0].spec).toMatchObject({ shots: ['海边露台', '石板街道', '海边咖啡馆'], skip_preview: true, duration_seconds: 5, ratio: '16:9' })
})

test('one length applies to every shot and the total says so', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/create/video-sequence')
  await fill(page, ['海边露台', '石板街道', ''])
  await page.getByRole('combobox', { name: '每个镜头时长' }).selectOption('8')
  await expect(page.getByText('16 秒（2 个镜头 × 8 秒）')).toBeVisible()
  await expect(page.getByRole('listitem', { name: '镜头 1' }).getByText('8 秒')).toBeVisible()
})

test('manual references are picked on re-anchoring shots and cleared with a notice when moved', async ({ page }) => {
  await useDemoApi(page)
  const jobs = captureJobs(page)
  await page.goto('/create/video-sequence')
  await page.getByRole('button', { name: '添加镜头' }).click()
  await fill(page, ['海边露台', '石板街道', '海边咖啡馆', '日落的码头'])
  await page.getByText('高级设置').click()
  await page.getByRole('switch', { name: '叙事连贯' }).click()
  await page.getByRole('radiogroup', { name: '参考策略' }).getByRole('radio', { name: '手动' }).click()
  await expect(page.getByRole('combobox', { name: '镜头 2 参考的镜头' })).toHaveCount(0)
  await page.getByRole('combobox', { name: '镜头 4 参考的镜头' }).selectOption({ label: '镜头 2' })
  await page.getByRole('button', { name: '生成预览（4 个镜头）' }).click()
  await expect.poll(() => jobs.length).toBe(1)
  expect(jobs[0].spec).toMatchObject({ narrative_continuity: true, reference_selection_mode: 'manual', shot_reference_overrides: [0, 0, 0, 2] })
  // moving the picked shot below the one that refers to it clears the pick and says which
  await page.getByRole('listitem', { name: '镜头 2' }).getByRole('button', { name: '下移' }).click()
  await page.getByRole('listitem', { name: '镜头 3' }).getByRole('button', { name: '下移' }).click()
  await expect(page.getByRole('status').filter({ hasText: '镜头 3 的参考已清除：镜头 4 现在排在它后面。' })).toBeVisible()
})

test('after the previews are generated the page points to the review', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/create/video-sequence')
  await fill(page, ['海边露台', '石板街道', '海边咖啡馆'])
  await page.getByRole('button', { name: '生成预览（3 个镜头）' }).click()
  await expect(page.getByText('预览已生成，请逐镜头确认。')).toBeVisible({ timeout: 10_000 })
  await expect(page.getByRole('listitem', { name: '镜头 2' }).getByLabel('镜头 2').first()).toBeVisible()
  await page.getByRole('link', { name: '去确认' }).click()
  await expect(page.getByRole('heading', { name: '待你确认' })).toBeVisible()
})

test('the storyboard is fully translated in English', async ({ page }) => {
  await useDemoApi(page, { lang: 'en' })
  await page.goto('/create/video-sequence')
  await expect(page.getByRole('heading', { name: 'Storyboard', level: 1 })).toBeVisible()
  await page.getByText('Advanced').click()
  const untranslated = await page.evaluate(() => {
    const main = document.querySelector('main')!.cloneNode(true) as HTMLElement
    main.querySelectorAll('select, option, textarea, input').forEach((el) => el.remove())
    return [...main.querySelectorAll('button, label, h1, h2, h3, legend, p, span, summary')]
      .map((el) => el.textContent ?? '')
      .filter((text) => /[一-鿿]/.test(text))
  })
  expect(untranslated).toEqual([])
})
