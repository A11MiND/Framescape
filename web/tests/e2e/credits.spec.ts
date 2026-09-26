import { test, expect } from '@playwright/test'
import { useDemoApi } from './demo'
import { createDemoApi } from '../../src/demo/api'

test('available and reserved credits, and how reserve, charge and release relate', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/credits')
  await expect(page.getByText('可用积分', { exact: true }).locator('..')).toContainText('860')
  await expect(page.getByText('预留积分', { exact: true }).locator('..')).toContainText('350')
  await expect(page.getByText('预留不是消耗')).toBeVisible()
  await expect(page.getByText('实际用量超过预留时，超出部分从可用积分补扣，并在记录里单列。')).toBeVisible()
  await expect(page.getByRole('note')).toContainText('金额不能相加')
})

test('each ledger event names its type, task and meaning', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/credits')
  const rows = page.getByRole('row')
  const reserve = rows.filter({ hasText: '提交任务时按报价预留' }).first()
  await expect(reserve).toContainText('预留')
  await expect(reserve).toContainText('预留 40')
  await expect(reserve.getByRole('link', { name: '柠檬汽水四格漫画' })).toHaveAttribute('href', '/jobs/J005')
  await expect(rows.filter({ hasText: '其中 1 超出预留，从可用积分补扣' })).toContainText('−21')
  await expect(rows.filter({ hasText: '未用完的预留退回' })).toContainText('释放 9')
  await expect(rows.filter({ hasText: '连续发布 3 天奖励' })).toContainText('+10')
  await expect(rows.filter({ hasText: '管理员发放 · 内测赠送' })).toContainText('+500')
})

test('the demo top-up says no payment is taken and adds a ledger row', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/credits')
  await expect(page.getByText('非真实支付：不收取任何费用，只用于演示和测试。')).toBeVisible()
  await page.getByRole('button', { name: '+200 演示充值' }).click()
  await expect(page.getByText('已演示充值 200 积分')).toBeVisible()
  await expect(page.getByText('可用积分', { exact: true }).locator('..')).toContainText('1,060')
  await expect(page.getByRole('row').nth(1)).toContainText('演示充值（非真实支付）')
  await expect(page.getByRole('row').nth(1)).toContainText('+200')
})

test('accounts that cannot top up are told so, and an empty history explains itself', async ({ page }) => {
  await useDemoApi(page)
  const me = createDemoApi((f) => f)('GET', '/me', '', undefined).body as Record<string, unknown>
  await page.route('**/api/v1/me', (route) => route.fulfill({ json: { ...me, is_admin: false } }))
  await page.route('**/api/v1/credits/ledger**', (route) => route.fulfill({ json: { entries: [] } }))
  await page.goto('/credits')
  await expect(page.getByText('充值暂未开放，需要积分请联系管理员。')).toBeVisible()
  await expect(page.getByRole('button', { name: /演示充值/ })).toHaveCount(0)
  await expect(page.getByText('还没有积分记录')).toBeVisible()
})

test('phones list the history as cards', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await useDemoApi(page)
  await page.goto('/credits')
  const card = page.getByRole('listitem').filter({ hasText: '未用完的预留退回' })
  await expect(card).toContainText('释放')
  await expect(card.getByRole('link', { name: '柠檬汽水四格漫画' })).toBeVisible()
})

test('English: credits read in English', async ({ page }) => {
  await useDemoApi(page, { lang: 'en' })
  await page.goto('/credits')
  await expect(page.getByRole('heading', { name: 'Credits', level: 1 })).toBeVisible()
  await expect(page.getByText('Reserved is not spent')).toBeVisible()
  await expect(page.getByRole('row').filter({ hasText: 'Unused reservation returned' })).toContainText('Release 9')
})
