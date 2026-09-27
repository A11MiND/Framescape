import { test, expect } from '@playwright/test'
import { useDemoApi } from './demo'
import { createDemoApi } from '../../src/demo/api'

test('changing the password checks its rules and the current password in place', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/settings')
  const current = page.getByLabel('当前密码', { exact: true })
  const next = page.getByLabel('新密码', { exact: true })
  const confirm = page.getByLabel('确认新密码', { exact: true })
  await page.getByRole('button', { name: '保存密码' }).click()
  await expect(page.getByText('请输入当前密码')).toBeVisible()
  await expect(page.getByText('新密码至少 8 个字符')).toBeVisible()

  await current.fill('wrong-password')
  await next.fill('new-pass-2026')
  await confirm.fill('new-pass-2025')
  await expect(page.getByText('强度参考')).toBeVisible()
  await page.getByRole('button', { name: '保存密码' }).click()
  await expect(page.getByText('两次输入的新密码不一致')).toBeVisible()
  await confirm.fill('new-pass-2026')
  await page.getByRole('button', { name: '保存密码' }).click()
  await expect(page.getByText('当前密码不正确')).toBeVisible()
  await expect(next).toHaveValue('new-pass-2026')

  await current.fill('demo-password')
  await page.getByRole('button', { name: '保存密码' }).click()
  await expect(page.getByRole('status').filter({ hasText: '密码已更新' })).toBeVisible()
  await expect(current).toHaveValue('')
})

test('passwords can be shown and hidden', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/settings')
  const next = page.getByLabel('新密码', { exact: true })
  await next.fill('secret-123')
  await expect(next).toHaveAttribute('type', 'password')
  await page.getByRole('button', { name: '显示密码' }).nth(1).click()
  await expect(next).toHaveAttribute('type', 'text')
  await page.getByRole('button', { name: '隐藏密码' }).click()
  await expect(next).toHaveAttribute('type', 'password')
})

test('accounts without a password are told so', async ({ page }) => {
  await useDemoApi(page)
  const me = createDemoApi((f) => f)('GET', '/me', '', undefined).body as Record<string, unknown>
  await page.route('**/api/v1/me', (route) => route.fulfill({ json: { ...me, email: null, phone: '+8613800001234', has_password: false } }))
  await page.goto('/settings')
  await expect(page.getByText('这个账户用手机验证码或 Google 登录，没有密码可以修改。')).toBeVisible()
  await expect(page.getByLabel('手机号')).toHaveValue('+8613800001234')
  await expect(page.getByRole('button', { name: '保存密码' })).toHaveCount(0)
})

test('theme and language switch without losing what is being edited', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/create/image')
  await page.getByRole('combobox', { name: '描述画面' }).fill('海边的红色电车')
  await page.waitForTimeout(500)
  await page.getByRole('navigation').getByRole('link', { name: '设置' }).first().click()

  await page.getByRole('radio', { name: '深色' }).click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await page.getByRole('radio', { name: /跟随系统/ }).click()
  await expect(page.getByText(/当前为(浅色|深色)/)).toBeVisible()
  await page.getByRole('radio', { name: 'English' }).click()
  await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible()
  await expect(page.getByText('your prompts, character names and project names are not translated')).toBeVisible()

  await page.getByRole('navigation').getByRole('link', { name: 'Create' }).first().click()
  await expect(page.getByRole('combobox', { name: 'Describe the image' })).toHaveValue('海边的红色电车')
})

test('Traditional Chinese and avatar upload are available in settings', async ({ page }) => {
  await useDemoApi(page)
  await page.route('**/mock-upload', (route) => route.fulfill({ status: 200 }))
  await page.goto('/settings')
  await page.getByRole('radio', { name: '繁體中文' }).click()
  await expect(page.getByRole('heading', { name: '設置', level: 1 })).toBeVisible()
  await page.getByLabel('上傳頭像').setInputFiles({ name: 'avatar.png', mimeType: 'image/png', buffer: Buffer.from('png') })
  await expect(page.getByRole('status').filter({ hasText: '頭像已保存' })).toBeVisible()
})

test('signing out returns to the sign-in page', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/settings')
  await page.getByRole('button', { name: '退出登录' }).click()
  await expect(page).toHaveURL(/\/login/)
})
