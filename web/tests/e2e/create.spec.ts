import { test, expect } from '@playwright/test'
import { useDemoApi } from './demo'
import { createDemoApi } from '../../src/demo/api'

test('image mode: server quote, reviewed rewrite and results in the workspace', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/create/image')
  await expect(page.getByRole('link', { name: 'GPT 生图' })).toBeVisible()
  const prompt = page.getByRole('combobox', { name: '描述画面' })
  await prompt.fill('海边的红色电车')
  await expect(page.getByText('10 积分')).toBeVisible()
  await page.getByRole('radio', { name: '4' }).click()
  await expect(page.getByText('40 积分')).toBeVisible()

  await page.getByRole('button', { name: 'AI 润色' }).click()
  const dialog = page.getByRole('dialog', { name: '润色建议' })
  await expect(dialog).toContainText('清晨柔和的光线')
  await dialog.getByRole('button', { name: '保留原文' }).click()
  await expect(prompt).toHaveValue('海边的红色电车')
  await page.getByRole('button', { name: 'AI 润色' }).click()
  await page.getByRole('dialog', { name: '润色建议' }).getByRole('button', { name: '接受建议' }).click()
  await expect(prompt).toHaveValue(/清晨柔和的光线/)

  await page.getByRole('button', { name: '生成图片' }).click()
  await expect(page).toHaveURL(/\/create\/image$/)
  await expect(page.getByRole('link', { name: '查看任务' })).toBeVisible()
  await expect(page.getByRole('button', { name: /查看第 \d 个结果/ })).toHaveCount(4, { timeout: 10_000 })
})

test('mentions: # attaches a library image, @ binds a character', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/create/image')
  const prompt = page.getByRole('combobox', { name: '描述画面' })
  await prompt.fill('参考 #')
  await expect(page.getByRole('listbox', { name: '最近的图片' })).toBeVisible()
  await page.keyboard.press('Enter')
  await expect(prompt).toHaveValue('参考 图1 ')
  await expect(page.getByRole('button', { name: '移除参考图 1' })).toBeVisible()
  await prompt.pressSequentially('和 @小')
  await page.getByRole('option', { name: '@小悠' }).click()
  await expect(page.getByRole('button', { name: '移除角色 小悠' })).toBeVisible()
})

test('drafts are kept per mode', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/create/image')
  await page.getByRole('combobox', { name: '描述画面' }).fill('图片模式的草稿')
  await page.waitForTimeout(400)
  await page.getByRole('link', { name: 'GPT 生图' }).click()
  await expect(page.getByRole('combobox', { name: '描述画面' })).toHaveValue('')
  await page.getByRole('link', { name: '单图与批量' }).click()
  await expect(page.getByRole('combobox', { name: '描述画面' })).toHaveValue('图片模式的草稿')
})

test('GPT image: model shown, options from capabilities, sent-content preview, cost reviewed before generating', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/create/gpt')
  await expect(page.getByText('OpenAI · gpt-image-2.5-flare')).toBeVisible()
  await page.getByRole('combobox', { name: '描述画面' }).fill('一座灯塔')
  const sizes = page.getByRole('radiogroup', { name: '尺寸' }).getByRole('radio')
  await expect(sizes).toHaveText(['方形 1:1', '横向 3:2', '纵向 2:3'])
  await sizes.filter({ hasText: '横向 3:2' }).click()
  await page.getByRole('combobox', { name: '质量' }).selectOption('medium')
  await page.getByRole('button', { name: '选择预设' }).click()
  await page.getByRole('button', { name: /水彩/ }).click()
  await page.getByRole('button', { name: '完成' }).click()
  await page.getByRole('button', { name: '查看实际发送内容' }).click()
  await expect(page.getByText('一座灯塔，水彩质感，手绘笔触，柔和的色彩过渡')).toBeVisible()
  await expect(page.getByText('18 积分')).toBeVisible()
  await page.getByRole('button', { name: '检查费用' }).click()
  const dialog = page.getByRole('dialog', { name: '确认生成' })
  await expect(dialog).toContainText('gpt-image-2.5-flare')
  await expect(dialog).toContainText('标准')
  await expect(dialog).toContainText('横向 3:2')
  await expect(dialog).toContainText('预留不是用量上限')
  await dialog.getByRole('button', { name: '确认生成' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('link', { name: '查看任务' })).toBeVisible()
})

test('GPT image without beta access explains why and keeps the entry visible', async ({ page }) => {
  await useDemoApi(page)
  await page.route('**/api/v1/me', (route) =>
    route.fulfill({ json: { biz_id: 'u2', email: 'x@example.com', phone: null, balance: 10, held: 0, is_admin: false, entitlements: [] } }),
  )
  await page.goto('/create/image')
  await expect(page.getByRole('link', { name: /GPT 生图\s*内测未开通/ })).toBeVisible()
  await page.getByRole('link', { name: /GPT 生图/ }).click()
  await expect(page.getByRole('heading', { name: 'GPT 生图内测未开通' })).toBeVisible()
})

test('GPT image on a deployment without OpenAI says it cannot generate', async ({ page }) => {
  await useDemoApi(page)
  const caps = createDemoApi((f) => f)('GET', '/capabilities', '', undefined).body as { providers: { openai: { enabled: boolean } } }
  caps.providers.openai.enabled = false
  await page.route('**/api/v1/capabilities', (route) => route.fulfill({ json: caps }))
  await page.goto('/create/gpt')
  await expect(page.getByRole('link', { name: /GPT 生图\s*暂不可生成/ })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'GPT 生图暂不可生成' })).toBeVisible()
  await expect(page.getByRole('button', { name: '检查费用' })).toHaveCount(0)
})

test('a failed quote never shows a price and blocks generating', async ({ page }) => {
  await useDemoApi(page)
  await page.route('**/api/v1/jobs/estimate', (route) => route.fulfill({ status: 500, json: { code: 'internal', message: 'x' } }))
  await page.goto('/create/image')
  await page.getByRole('combobox', { name: '描述画面' }).fill('海边')
  await expect(page.getByText('暂时无法计算费用')).toBeVisible()
  await expect(page.getByRole('button', { name: '生成图片' })).toBeDisabled()
})

test('guests get one free try in the workspace', async ({ page }) => {
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname.split('/api/v1')[1]
    if (path === '/trial/image') return route.fulfill({ json: { image_url: '/src/demo/assets/tram-hero.jpg' } })
    if (path === '/capabilities') return route.fulfill({ json: { image: { max_n: 9, max_prompt_chars: 1500 }, video: { duration_min: 4, duration_max: 15, max_prompt_chars: 7000, resolutions: [], ratios: [] } } })
    return route.fulfill({ status: 401, json: { code: 'unauthorized', message: 'x' } })
  })
  await page.goto('/create/image')
  await page.getByRole('combobox', { name: '描述画面' }).fill('海边的电车')
  await page.getByRole('button', { name: '免费试用一次' }).click()
  await expect(page.getByRole('heading', { name: '试用结果' })).toBeVisible()
})
