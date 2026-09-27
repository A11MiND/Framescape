import { test, expect } from '@playwright/test'
import { useDemoApi } from './demo'
import { createDemoApi } from '../../src/demo/api'

const caps = createDemoApi((f) => f)('GET', '/capabilities', '', undefined).body as Record<string, unknown>
test('unconfigured sign-in methods are hidden, while guest community stays open', async ({ page }) => {
  await useDemoApi(page, { guest: true })
  await page.goto('/login')
  await expect(page.getByRole('textbox', { name: '邮箱' })).toBeVisible()
  await expect(page.getByRole('button', { name: '使用 Google 登录' })).toHaveCount(0)
  await expect(page.getByRole('radio', { name: '手机验证码' })).toHaveCount(0)
  await expect(page.getByLabel('验证码')).toHaveCount(0)
  await page.getByRole('link', { name: '浏览社区' }).click()
  await expect(page).toHaveURL(/community/)
})
test('failed sign-in preserves inputs and successful sign-in returns to the requested page', async ({ page }) => {
  await useDemoApi(page, { guest: true })
  await page.goto('/projects')
  await expect(page).toHaveURL(/login/)
  await page.getByLabel('邮箱').fill('person@example.com')
  await page.getByLabel(/^密码/).fill('secret-password')
  await page.route('**/api/v1/auth/login', (r) =>
    r.fulfill({ status: 401, json: { code: 'invalid_credentials', message: 'DO NOT DISPLAY RAW' } }),
  )
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(page.getByLabel(/^密码/)).toHaveValue('secret-password')
  await expect(page.getByText('DO NOT DISPLAY RAW')).toHaveCount(0)
  await page.unroute('**/api/v1/auth/login')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).toHaveURL(/projects/)
})
test('registration validates UTF-8 password bytes and shows password controls', async ({ page }) => {
  await useDemoApi(page, { guest: true })
  await page.goto('/login')
  await page.getByRole('tab', { name: '注册' }).click()
  await page.getByLabel('邮箱').fill('new@example.com')
  await page.getByLabel(/^密码/).fill('字'.repeat(25))
  let called = false
  page.on('request', (r) => {
    if (r.url().endsWith('/auth/register')) called = true
  })
  await page.getByRole('button', { name: '注册', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('72')
  expect(called).toBe(false)
  await page.getByRole('button', { name: '显示密码' }).click()
  await expect(page.getByLabel(/^密码/)).toHaveAttribute('type', 'text')
})
test('configured email verification has a resend countdown and inline code errors', async ({ page }) => {
  await useDemoApi(page, { guest: true })
  await page.route('**/api/v1/capabilities', (r) =>
    r.fulfill({
      json: { ...caps, auth: { email_password: true, email_verification: true, phone_sms: true, google: false, guest_trial: true } },
    }),
  )
  await page.goto('/login')
  await page.getByRole('tab', { name: '注册' }).click()
  await page.getByLabel('邮箱').fill('new@example.com')
  await page.getByLabel(/^密码/).fill('password-123')
  await page.getByRole('button', { name: '发送验证码' }).click()
  await expect(page.getByRole('button', { name: /秒后重发/ })).toBeDisabled()
  await page.getByRole('button', { name: '注册', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('6 位')
  await page.getByRole('radio', { name: '手机验证码' }).click()
  await expect(page.getByLabel('手机号码')).toBeVisible()
})
test('English login and capabilities failure recovery', async ({ page }) => {
  await useDemoApi(page, { guest: true, lang: 'en' })
  await page.route('**/api/v1/capabilities', (r) => r.fulfill({ status: 503, json: { code: 'internal' } }))
  await page.goto('/login')
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible({ timeout: 15000 })
  await page.unroute('**/api/v1/capabilities')
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeVisible()
  await expect(page.getByLabel(/^Password/)).toBeVisible()
})
