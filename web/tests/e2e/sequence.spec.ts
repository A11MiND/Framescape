import { test, expect, type Page } from '@playwright/test'
import { useDemoApi } from './demo'

function captureJobs(page: Page) {
  const bodies: Record<string, unknown>[] = []
  page.on('request', (r) => {
    if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/jobs') bodies.push(r.postDataJSON())
  })
  return bodies
}

const shot = (page: Page, n: number) => page.getByRole('textbox', { name: `画面 ${n}`, exact: true })
const refOf = (page: Page, n: number) => page.getByRole('combobox', { name: `画面 ${n} 引用的画面` })

test('references follow their shot, empty shots are skipped and results land in the same cards', async ({ page }) => {
  await useDemoApi(page)
  const jobs = captureJobs(page)
  await page.goto('/create/image-sequence')
  await page.getByText('快速独立', { exact: true }).click()
  await expect(page.getByRole('radio', { name: /快速独立/ })).toBeChecked()
  await shot(page, 1).fill('清晨的海港')
  await page.getByRole('button', { name: '添加画面' }).click()
  await page.getByRole('button', { name: '添加画面' }).click()
  await shot(page, 3).fill('电车驶过海边街道')
  await shot(page, 4).fill('黄昏的山坡小站')
  // a later shot is listed but cannot be chosen
  await expect(refOf(page, 2).locator('option', { hasText: '画面 3（排在后面，不能引用）' })).toBeDisabled()
  await refOf(page, 4).selectOption({ label: '画面 1' })
  await expect(page.getByText('共 3 张，1 个空白画面不生成')).toBeVisible()
  await page.getByRole('button', { name: '生成系列' }).click()
  await expect.poll(() => jobs.length).toBe(1)
  expect(jobs[0].spec).toMatchObject({ shots: ['清晨的海港', '电车驶过海边街道', '黄昏的山坡小站'], shot_source_refs: [0, 0, 1] })
  expect((jobs[0].spec as Record<string, unknown>).image_sequence_mode).toBeUndefined()
  // node shot-N maps back to the Nth sent shot, not the Nth card
  await expect(page.getByRole('listitem', { name: '画面 4' }).getByRole('img', { name: '画面 4' })).toBeVisible({ timeout: 8000 })
  await expect(page.getByRole('listitem', { name: '画面 2' }).getByRole('img')).toHaveCount(0)
})

test('reordering clears a reference that would point forward and names the shots', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/create/image-sequence')
  await shot(page, 1).fill('海港')
  await shot(page, 2).fill('电车')
  await refOf(page, 2).selectOption({ label: '画面 1' })
  await page.getByRole('listitem', { name: '画面 2' }).getByRole('button', { name: '上移' }).click()
  await expect(page.getByRole('status').filter({ hasText: '画面 1 的引用已清除：画面 2 现在排在它后面。' })).toBeVisible()
  await expect(shot(page, 1)).toHaveValue('电车')
  // the keyboard moves a shot too, and a still-valid reference is kept
  await refOf(page, 2).selectOption({ label: '画面 1' })
  await page.getByRole('button', { name: '添加画面' }).click()
  await shot(page, 3).fill('小站')
  await shot(page, 3).press('Alt+ArrowUp')
  await expect(shot(page, 2)).toHaveValue('小站')
  await expect(shot(page, 2)).toBeFocused()
  await expect(refOf(page, 3)).toHaveValue(/.+/)
  await expect(refOf(page, 3).locator('option:checked')).toHaveText('画面 1')
})

test('deleting a referenced shot tells which reference was cleared', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/create/image-sequence')
  await shot(page, 1).fill('海港')
  await shot(page, 2).fill('电车')
  await refOf(page, 2).selectOption({ label: '画面 1' })
  await page.getByRole('button', { name: '画面 1 的更多操作' }).click()
  await page.getByRole('menuitem', { name: '删除画面' }).click()
  await expect(page.getByRole('status').filter({ hasText: '画面 1 的引用已清除：它引用的画面 1 已删除。' })).toBeVisible()
})

test('a sequence is capped at the deployment limit', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/create/image-sequence')
  for (let i = 0; i < 10; i++) await page.getByRole('button', { name: '添加画面' }).click()
  await expect(page.getByRole('button', { name: '最多 12 个画面' })).toBeDisabled()
})

test('GPT sequences start from the GPT page and are reviewed before generating', async ({ page }) => {
  await useDemoApi(page)
  const jobs = captureJobs(page)
  await page.goto('/create/gpt')
  await page.getByRole('radiogroup', { name: '输出' }).getByRole('radio', { name: '连续图片' }).click()
  await expect(page).toHaveURL(/\/create\/image-sequence$/)
  await expect(page.getByRole('radiogroup', { name: '生成服务' }).getByRole('radio', { name: 'GPT' })).toHaveAttribute('aria-checked', 'true')
  await page.getByRole('radiogroup', { name: '尺寸' }).getByRole('radio', { name: /横向 3:2/ }).click()
  await shot(page, 1).fill('海港')
  await shot(page, 2).fill('电车')
  await page.getByRole('button', { name: '检查费用' }).click()
  const dialog = page.getByRole('dialog', { name: '确认生成' })
  await expect(dialog).toContainText('gpt-image-2.5-flare')
  await expect(dialog).toContainText('横向 3:2')
  await dialog.getByRole('button', { name: '确认生成' }).click()
  await expect.poll(() => jobs.length).toBe(1)
  expect(jobs[0].spec).toMatchObject({ image_provider: 'openai', image_size: '1536x1024', image_sequence_mode: 'continuity', shots: ['海港', '电车'] })
})

test('without GPT access the provider is shown but locked with the reason', async ({ page }) => {
  await useDemoApi(page)
  await page.route('**/api/v1/me', (route) =>
    route.fulfill({ json: { biz_id: 'u2', email: 'x@example.com', phone: null, balance: 10, held: 0, is_admin: false, entitlements: [] } }),
  )
  await page.goto('/create/image-sequence')
  await expect(page.getByRole('radiogroup', { name: '生成服务' }).getByRole('radio', { name: 'GPT' })).toBeDisabled()
  await expect(page.getByText('GPT 暂不可用于连续图片：内测未开通')).toBeVisible()
})

test('the sequence page is fully translated in English', async ({ page }) => {
  await useDemoApi(page, { lang: 'en' })
  await page.goto('/create/image-sequence')
  await expect(page.getByRole('heading', { name: 'Image sequence', level: 1 })).toBeVisible()
  await page.getByRole('textbox', { name: 'Image 1', exact: true }).fill('harbour')
  await expect(page.getByRole('combobox', { name: 'What image 2 builds on' })).toBeVisible()
  const untranslated = await page.evaluate(() => {
    const main = document.querySelector('main')!.cloneNode(true) as HTMLElement
    main.querySelectorAll('select, option, textarea, input').forEach((el) => el.remove())
    return [...main.querySelectorAll('button, label, h1, h2, h3, legend, p, span')]
      .map((el) => el.textContent ?? '')
      .filter((text) => /[一-鿿]/.test(text))
  })
  expect(untranslated).toEqual([])
})
