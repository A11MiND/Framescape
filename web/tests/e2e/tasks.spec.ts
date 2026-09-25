import { test, expect } from '@playwright/test'
import { useDemoApi } from './demo'

test('pins tasks waiting for review above the rest and counts from one source', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/jobs')
  const pinned = page.getByRole('region', { name: '待你确认' })
  await expect(pinned.getByRole('link', { name: '海岸电车之旅 · 宣传视频' }).first()).toBeVisible()
  await expect(pinned.getByRole('link', { name: '雪山晨雾 · 风格测试' }).first()).toBeVisible()
  // only the pinned row: the main list excludes it (hidden mobile cards are not in the accessibility tree)
  await expect(page.getByRole('link', { name: '海岸电车之旅 · 宣传视频' })).toHaveCount(1)
  await expect(page.getByRole('button', { name: /待确认\s*2/ })).toBeVisible()
  await expect(page.getByRole('button', { name: /进行中\s*3/ })).toBeVisible()
  await expect(page.getByRole('button', { name: /失败\s*2/ })).toBeVisible()
})

test('failed tasks show a localized reason, filters live in the URL', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/jobs')
  await page.getByRole('button', { name: /失败\s*2/ }).click()
  await expect(page).toHaveURL(/bucket=failed/)
  const row = page.getByRole('row', { name: /城市夜景短片/ })
  await expect(row).toContainText('描述涉及敏感内容')
  await expect(row.getByRole('link', { name: '查看原因' })).toBeVisible()
  await page.getByRole('combobox', { name: '状态' }).selectOption('cancelled')
  await expect(page).toHaveURL(/status=cancelled/)
  await expect(page.getByRole('row', { name: /海岸电车 · 5 秒镜头/ })).toBeVisible()
  await expect(page.getByRole('row', { name: /城市夜景短片/ })).toHaveCount(0)
})

test('cancelling names the task and its effect, then waits for the server', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/jobs?bucket=active')
  const row = page.getByRole('row', { name: /雪山湖泊全景/ })
  await row.getByRole('button', { name: '更多操作' }).click()
  await page.getByRole('menuitem', { name: '取消任务' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('雪山湖泊全景')
  await expect(dialog).toContainText('已产生的费用照常结算，剩余预留退回')
  await dialog.getByRole('button', { name: '取消任务' }).click()
  await expect(page.getByText('已请求取消，等待服务端确认')).toBeVisible()
  await expect(page.getByRole('row', { name: /雪山湖泊全景/ })).toContainText('取消中')
})

test('English labels and a phone layout without horizontal scroll', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 })
  await useDemoApi(page, { lang: 'en' })
  await page.goto('/jobs')
  await expect(page.getByRole('heading', { name: 'Tasks', level: 1 })).toBeVisible()
  await expect(page.getByRole('table')).toBeHidden()
  await expect(page.getByRole('listitem').filter({ hasText: 'Needs review' }).first()).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
})
