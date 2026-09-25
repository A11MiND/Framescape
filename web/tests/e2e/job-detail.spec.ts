import { test, expect } from '@playwright/test'
import { useDemoApi } from './demo'
import { createDemoApi } from '../../src/demo/api'

test('a finished task shows results, credits and settings, and confirms publishing', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/jobs/J013')
  await expect(page.getByRole('heading', { name: '阳光下的海岸电车', level: 1 })).toBeVisible()
  await expect(page.getByRole('button', { name: /查看第 \d 个结果/ })).toHaveCount(4)
  const credits = page.getByRole('heading', { name: '积分' }).locator('..')
  await expect(credits).toContainText('累计预留40')
  await expect(credits).toContainText('累计结算36')
  await expect(credits).toContainText('已释放4')
  await expect(credits).toContainText('当前冻结0')
  const params = page.locator('summary', { hasText: '4:3' })
  await expect(params).toContainText('图片 · 4 · 4:3')
  await params.click()
  await expect(page.getByRole('definition').filter({ hasText: /^4:3$/ })).toBeVisible()
  await page.getByRole('button', { name: '发布到社区' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('参考图等私有素材不会公开')
  await dialog.getByRole('button', { name: '发布' }).click()
  await expect(page.getByText('已发布到社区')).toBeVisible()
})

test('a failed task explains the reason by code and offers to generate again', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/jobs/J007')
  await expect(page.getByRole('heading', { name: '生成失败' })).toBeVisible()
  await expect(page.getByText('描述涉及敏感内容，请修改后重试。').first()).toBeVisible()
  await expect(page.getByRole('button', { name: '修改并重新生成' })).toBeVisible()
  // Content the provider refused would be refused again: no retry with the same input.
  await page.getByText('执行详情').click()
  await expect(page.getByRole('button', { name: '重试此步（新建任务）' })).toHaveCount(0)
})

test('a step that failed for a transient reason can be retried as a new task', async ({ page }) => {
  await useDemoApi(page)
  const detail = createDemoApi((f) => `/src/demo/assets/${f}`)('GET', '/jobs/J007', '', undefined).body as { error_code: string; nodes: { error_code: string; retryable?: boolean }[] }
  detail.error_code = 'provider_busy'
  detail.nodes = detail.nodes.map((n) => ({ ...n, error_code: 'provider_busy', retryable: true }))
  await page.route('**/api/v1/jobs/J007', (route) => route.fulfill({ json: detail }))
  let retried = 0
  await page.route('**/api/v1/jobs/J007/nodes/gen/retry', (route) => {
    retried++
    return route.fulfill({ json: { biz_id: 'J013' } })
  })
  await page.goto('/jobs/J007')
  await expect(page.getByText('生成服务繁忙，多次重试后仍未完成。').first()).toBeVisible()
  await page.getByRole('button', { name: '重试此步（新建任务）' }).first().click()
  await expect(page).toHaveURL(/\/jobs\/J013$/)
  expect(retried).toBe(1)
})

test('a partially finished task keeps the panels that worked and names the one that failed', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/jobs/J005')
  await expect(page.getByRole('heading', { name: '部分步骤失败' })).toBeVisible()
  await expect(page.getByRole('listitem').filter({ hasText: '第 4 格' }).first()).toContainText('生成服务繁忙，多次重试后仍未完成。')
  await expect(page.getByRole('button', { name: /查看第 \d 个结果/ })).toHaveCount(3)
  await expect(page.getByRole('button', { name: '重试此步（新建任务）' })).toHaveCount(0)
  const credits = page.getByRole('heading', { name: '积分' }).locator('..')
  await expect(credits).toContainText('累计结算31')
  await expect(credits).toContainText('已释放9')
})

test('a running task shows the lost connection and reconnects on request', async ({ page }) => {
  await useDemoApi(page)
  let streamCalls = 0
  await page.route('**/api/v1/stream', (route) => {
    streamCalls++
    return route.abort()
  })
  await page.goto('/jobs/J009')
  await expect(page.getByRole('heading', { name: '正在生成' })).toBeVisible()
  const notice = page.getByRole('status').filter({ hasText: '连接中断' })
  await expect(notice).toBeVisible()
  const before = streamCalls
  await notice.getByRole('button', { name: '重试连接' }).click()
  await expect.poll(() => streamCalls).toBeGreaterThan(before)
})

test('the preview review prices every choice on the server', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/jobs/J012')
  await expect(page.getByRole('heading', { name: '待你确认' })).toBeVisible()
  await expect(page.getByText(/请在 2026年9月30日/)).toBeVisible()
  await expect(page.getByRole('radiogroup', { name: /片段 \d 的处理/ })).toHaveCount(3)
  await page.getByRole('radiogroup', { name: '片段 2 的处理' }).getByRole('radio', { name: '重做' }).click()
  await expect(page.getByLabel('重做时使用的描述（可修改）')).toHaveValue('海边咖啡馆的露台，俯瞰平静的海湾。')
  await page.getByRole('radiogroup', { name: '片段 3 的处理' }).getByRole('radio', { name: '升级 2K' }).click()
  const quote = page.getByRole('complementary', { name: '报价' })
  await expect(quote).toContainText('重做 1 个片段（768P）40 积分')
  await expect(quote).toContainText('升级 1 个片段到 2K64 积分')
  await expect(quote).toContainText('本地合成，不收费')
  await expect(quote).toContainText('预计新增104 积分')
  await expect(quote).toContainText('全部升级 2K192 积分')
  await quote.getByRole('button', { name: '确认并合成' }).click()
  await expect(page.getByText('已提交，正在处理你的选择')).toBeVisible()
})

test('a changed price keeps the choices and asks to confirm again', async ({ page }) => {
  await useDemoApi(page)
  await page.route('**/api/v1/jobs/J012/resume', (route) =>
    route.fulfill({ status: 409, json: { code: 'price_changed', message: 'changed', params: { credits_total: 120 } } }),
  )
  await page.goto('/jobs/J012')
  await page.getByRole('radiogroup', { name: '片段 1 的处理' }).getByRole('radio', { name: '重做' }).click()
  await page.getByLabel('重做时使用的描述（可修改）').fill('电车在黄昏时驶入画面')
  const quote = page.getByRole('complementary', { name: '报价' })
  await expect(quote).toContainText('预计新增40 积分')
  await quote.getByRole('button', { name: '确认并合成' }).click()
  await expect(page.getByText('费用已变化，请确认新的报价。')).toBeVisible()
  await expect(page.getByRole('radiogroup', { name: '片段 1 的处理' }).getByRole('radio', { name: '重做' })).toHaveAttribute('aria-checked', 'true')
  await expect(page.getByLabel('重做时使用的描述（可修改）')).toHaveValue('电车在黄昏时驶入画面')
})
