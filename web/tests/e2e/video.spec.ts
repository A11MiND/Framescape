import { test, expect, type Page } from '@playwright/test'
import { useDemoApi } from './demo'

function captureJobs(page: Page) {
  const bodies: Record<string, unknown>[] = []
  page.on('request', (r) => {
    if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/jobs') bodies.push(r.postDataJSON())
  })
  return bodies
}

async function pickFromLibrary(page: Page, slot: number, item = 0) {
  await page.getByRole('button', { name: '从素材库选择' }).nth(slot).click()
  const dialog = page.getByRole('dialog', { name: '选择参考图' })
  await dialog.getByRole('listitem').nth(item).getByRole('button').click()
  await dialog.getByRole('button', { name: '使用所选' }).click()
}

test('text-to-video sends the chosen ratio; switching an empty mode does not ask', async ({ page }) => {
  await useDemoApi(page)
  const jobs = captureJobs(page)
  await page.goto('/create/video')
  await page.getByRole('combobox', { name: '运动描述' }).fill('电车缓缓驶入画面')
  await page.getByRole('radiogroup', { name: '画面比例' }).getByRole('radio', { name: '9:16' }).click()
  await page.getByRole('radio', { name: '首尾帧' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByText('跟随参考画面')).toBeVisible()
  await expect(page.getByText('请添加首帧')).toBeVisible()
  await expect(page.getByRole('button', { name: '生成视频' })).toBeDisabled()
  await page.getByRole('radio', { name: '文字生成' }).click()
  await page.getByRole('button', { name: '生成视频' }).click()
  await expect.poll(() => jobs.length).toBe(1)
  expect(jobs[0].spec).toEqual({ text: '电车缓缓驶入画面', duration_seconds: 6, resolution: '768P', ratio: '9:16' })
})

test('switching away from frames lists what will be cleared, and frames never travel with references', async ({ page }) => {
  await useDemoApi(page)
  const jobs = captureJobs(page)
  await page.goto('/create/video')
  await page.getByRole('combobox', { name: '运动描述' }).fill('镜头推进')
  await page.getByRole('radio', { name: '首尾帧' }).click()
  await pickFromLibrary(page, 0)
  await expect(page.getByRole('figure').filter({ hasText: '首帧' }).getByRole('img')).toBeVisible()
  await page.getByRole('radio', { name: '多素材参考' }).click()
  const confirm = page.getByRole('dialog', { name: '切换到「多素材参考」' })
  await expect(confirm).toContainText('首帧')
  await confirm.getByRole('button', { name: '取消' }).click()
  await expect(page.getByRole('radio', { name: '首尾帧' })).toHaveAttribute('aria-checked', 'true')
  await page.getByRole('radio', { name: '多素材参考' }).click()
  await page.getByRole('dialog').getByRole('button', { name: '清除并切换' }).click()
  await page.getByRole('tab', { name: '视频' }).click()
  await expect(page.getByText('最多 3 段，合计不超过 15 秒')).toBeVisible()
  await page.getByRole('button', { name: '从素材库选择' }).click()
  const videos = page.getByRole('dialog', { name: '选择参考视频' })
  await videos.getByRole('listitem').first().getByRole('button').click()
  await videos.getByRole('button', { name: '使用所选' }).click()
  await page.getByRole('button', { name: '生成视频' }).click()
  await expect.poll(() => jobs.length).toBe(1)
  expect(jobs[0].spec).toEqual({ text: '镜头推进', duration_seconds: 6, resolution: '768P', reference_video_asset_ids: ['clip-1'] })
})

test('prompt enhancement shows its quoted cost and is sent', async ({ page }) => {
  await useDemoApi(page)
  const jobs = captureJobs(page)
  await page.goto('/create/video')
  await page.getByRole('combobox', { name: '运动描述' }).fill('海浪拍岸')
  await page.getByRole('switch', { name: 'AI 增强提示词' }).click()
  await expect(page.getByText('另计 10 积分')).toBeVisible()
  await page.getByRole('button', { name: '生成视频' }).click()
  await expect.poll(() => jobs.length).toBe(1)
  expect(jobs[0].spec).toMatchObject({ prompt_enhance: true, ratio: '16:9' })
})

test('turning an image into a video opens frames mode with that image first', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/jobs/J013')
  await page.getByRole('button', { name: '图生视频' }).click()
  await expect(page).toHaveURL(/\/create\/video$/)
  await expect(page.getByRole('radio', { name: '首尾帧' })).toHaveAttribute('aria-checked', 'true')
  await expect(page.getByRole('figure').filter({ hasText: '首帧' }).getByRole('img')).toBeVisible()
})

test('the video page is fully translated in English', async ({ page }) => {
  await useDemoApi(page, { lang: 'en' })
  await page.goto('/create/video')
  await expect(page.getByRole('heading', { name: 'Single video', level: 1 })).toBeVisible()
  for (const mode of ['First & last frame', 'Reference media', 'Text only']) {
    await page.getByRole('radio', { name: mode }).click()
    const untranslated = await page.evaluate(() => {
      const main = document.querySelector('main')!.cloneNode(true) as HTMLElement
      main.querySelectorAll('select, option, textarea, input').forEach((el) => el.remove())
      return [...main.querySelectorAll('button, label, h1, h2, h3, legend, p, span, figcaption')]
        .map((el) => el.textContent ?? '')
        .filter((text) => /[一-鿿]/.test(text))
    })
    expect(untranslated).toEqual([])
  }
})

test('reference images and audio stop at the limits capabilities publish', async ({ page }) => {
  await useDemoApi(page)
  const audio = (i: number) => ({ biz_id: `audio-${i}`, type: 'audio', public_url: `/a${i}.mp3`, mime: 'audio/mpeg', width: 0, height: 0, resolution_tag: '', created_at: '2026-09-24T06:20:00Z', project_id: '', is_public: false })
  await page.route('**/api/v1/assets?*type=audio*', (route) => route.fulfill({ json: { assets: [1, 2, 3, 4].map(audio) } }))
  await page.route(/\/api\/v1\/assets\/audio-\d$/, (route) => route.fulfill({ json: { ...audio(Number(route.request().url().slice(-1))), thumb_url: '' } }))
  await page.goto('/create/video')
  await page.getByRole('combobox', { name: '运动描述' }).fill('参考素材的节奏')
  await page.getByRole('radio', { name: '多素材参考' }).click()

  // images: the 9th can be chosen, the 10th cannot
  await page.getByRole('button', { name: '从素材库选择' }).click()
  const images = page.getByRole('dialog', { name: '选择参考图' })
  const imageItems = images.getByRole('listitem').getByRole('button')
  for (let i = 0; i < 9; i++) await imageItems.nth(i).click()
  await expect(images.getByText('已选 9 / 9')).toBeVisible()
  await expect(imageItems.nth(9)).toBeDisabled()
  await images.getByRole('button', { name: '使用所选' }).click()
  await expect(page.getByRole('tab', { name: /图片\s*9/ })).toBeVisible()
  await expect(page.getByRole('tabpanel').getByRole('button', { name: '从素材库选择' })).toHaveCount(0)

  // audio: the 3rd can be chosen, the 4th cannot
  await page.getByRole('tab', { name: '音频' }).click()
  await page.getByRole('button', { name: '从素材库选择' }).click()
  const audios = page.getByRole('dialog', { name: '选择参考音频' })
  const audioItems = audios.getByRole('listitem').getByRole('button')
  for (let i = 0; i < 3; i++) await audioItems.nth(i).click()
  await expect(audioItems.nth(3)).toBeDisabled()
  await audios.getByRole('button', { name: '使用所选' }).click()
  await expect(page.getByRole('tab', { name: /音频\s*3/ })).toBeVisible()
})

test('a request over the server limit shows the localized limit', async ({ page }) => {
  await useDemoApi(page)
  await page.route('**/api/v1/jobs/estimate', (route) => route.fulfill({ status: 422, json: { code: 'reference_audios_too_many', message: 'x', params: { max: 3 } } }))
  await page.goto('/create/video')
  await page.getByRole('combobox', { name: '运动描述' }).fill('x')
  await expect(page.getByRole('alert').filter({ hasText: '参考音频最多 3 段。' })).toBeVisible()
  await expect(page.getByRole('button', { name: '生成视频' })).toBeDisabled()
})
