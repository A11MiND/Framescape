import { test, expect, type Page } from '@playwright/test'
import { useDemoApi } from './demo'

const card = (page: Page, name: string) => page.getByRole('listitem').filter({ has: page.getByRole('heading', { name, exact: true }) })

async function pickFirstReference(page: Page) {
  await page.getByRole('button', { name: '从素材库选择' }).click()
  const picker = page.getByRole('dialog', { name: '选择参考图' })
  await picker.getByRole('listitem').first().getByRole('button').click()
  await picker.getByRole('button', { name: '使用所选' }).click()
}

test('characters list their references and project, and search narrows them', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/characters')
  await expect(card(page, '阿橘')).toContainText('一只橘色的猫，喜欢晒太阳')
  await expect(card(page, '阿橘')).toContainText('海岸之城')
  await expect(card(page, '阿橘')).toContainText('2 张参考图')
  await page.getByRole('textbox', { name: '搜索角色名称或描述' }).fill('猫')
  await expect(card(page, '阿橘')).toBeVisible()
  await expect(card(page, '小悠')).toHaveCount(0)
  await page.getByRole('textbox', { name: '搜索角色名称或描述' }).fill('不存在')
  await expect(page.getByText('没有符合搜索的角色')).toBeVisible()
})

test('creating a character requires a name and a reference; the seed is optional', async ({ page }) => {
  await useDemoApi(page)
  const posted: Record<string, unknown>[] = []
  page.on('request', (r) => r.method() === 'POST' && r.url().endsWith('/api/v1/characters') && posted.push(r.postDataJSON()))
  await page.goto('/characters')
  await page.getByRole('button', { name: '新建角色' }).first().click()
  const drawer = page.getByRole('dialog', { name: '新建角色' })
  await drawer.getByRole('button', { name: '保存角色' }).click()
  await expect(drawer.getByText('请填写角色名称')).toBeVisible()
  await expect(drawer.getByText('请至少添加 1 张参考图片')).toBeVisible()
  expect(posted).toHaveLength(0)

  await drawer.getByRole('textbox', { name: /角色名称/ }).fill('阿辰')
  await drawer.getByRole('textbox', { name: /外观描述/ }).fill('短发的年轻男性，穿休闲外套')
  await pickFirstReference(page)
  await drawer.getByRole('combobox', { name: /所属项目/ }).selectOption({ label: '海岸之城' })
  await drawer.getByRole('button', { name: '保存角色' }).click()
  await expect(page.getByText('角色已创建')).toBeVisible()
  await expect(card(page, '阿辰')).toContainText('短发的年轻男性')
  expect(posted[0]).toMatchObject({ name: '阿辰', project_id: 'P1' })
  expect(posted[0].seed).toBeUndefined()
  expect((posted[0].ref_asset_ids as string[]).length).toBe(1)
})

test('editing keeps the seed unless changed, and refuses an out-of-range seed', async ({ page }) => {
  await useDemoApi(page)
  const patched: Record<string, unknown>[] = []
  page.on('request', (r) => r.method() === 'PATCH' && r.url().includes('/api/v1/characters/') && patched.push(r.postDataJSON()))
  await page.goto('/characters')
  await page.getByRole('button', { name: '阿橘 的更多操作' }).click()
  await page.getByRole('menuitem', { name: '编辑' }).click()
  const drawer = page.getByRole('dialog', { name: '编辑角色' })
  await expect(drawer.getByRole('textbox', { name: /角色名称/ })).toHaveValue('阿橘')
  await drawer.getByRole('button', { name: '高级设置' }).click()
  const seed = drawer.getByRole('textbox', { name: /随机种子/ })
  await expect(seed).toHaveValue('1024')
  await expect(drawer).toContainText('只对 MiniMax 生成生效，GPT 生图不使用种子')
  await seed.fill('99999999999')
  await drawer.getByRole('button', { name: '保存角色' }).click()
  await expect(drawer.getByText('种子必须是 0 到 2147483647 之间的整数')).toBeVisible()
  expect(patched).toHaveLength(0)
  await seed.fill('2048')
  await drawer.getByRole('textbox', { name: /角色名称/ }).fill('橘子')
  await drawer.getByRole('button', { name: '保存角色' }).click()
  await expect(page.getByText('角色已保存')).toBeVisible()
  await expect(card(page, '橘子')).toBeVisible()
  expect(patched[0]).toMatchObject({ name: '橘子', seed: 2048 })
})

test('deleting names the character and keeps its images', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/characters')
  await page.getByRole('button', { name: '小悠 的更多操作' }).click()
  await page.getByRole('menuitem', { name: '删除' }).click()
  const dialog = page.getByRole('alertdialog').or(page.getByRole('dialog'))
  await expect(dialog).toContainText('删除角色「小悠」？')
  await expect(dialog).toContainText('参考图仍保留在素材库')
  await dialog.getByRole('button', { name: '删除角色' }).click()
  await expect(page.getByText('角色已删除')).toBeVisible()
  await expect(card(page, '小悠')).toHaveCount(0)
})

test('using a character opens the last creation mode with it bound and the draft kept', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/create/video')
  await page.getByRole('combobox', { name: '运动描述' }).fill('海浪拍打礁石')
  await page.waitForTimeout(500)
  await page.getByRole('navigation').getByRole('link', { name: '角色' }).first().click()
  await card(page, '阿橘').getByRole('button', { name: '创作时使用' }).click()
  await expect(page).toHaveURL(/\/create\/video$/)
  await expect(page.getByText('@阿橘')).toBeVisible()
  await expect(page.getByRole('combobox', { name: '运动描述' })).toHaveValue('海浪拍打礁石')
})

test('the empty library uploads references and opens the form with them', async ({ page }) => {
  await useDemoApi(page)
  await page.route('**/api/v1/characters', (route) => (route.request().method() === 'GET' ? route.fulfill({ json: { characters: [] } }) : route.fallback()))
  await page.route('**/api/v1/assets/upload-url', (route) => route.fulfill({ json: { biz_id: 'portrait', upload_url: 'http://127.0.0.1:4173/fake-storage/p', storage_key: 'image/p.png' } }))
  await page.route('**/fake-storage/**', (route) => route.fulfill({ status: 200, body: '' }))
  await page.route('**/api/v1/assets/portrait/complete', (route) => route.fulfill({ json: { biz_id: 'portrait', type: 'image' } }))
  await page.goto('/characters')
  await expect(page.getByText('还没有角色')).toBeVisible()
  await expect(page.getByText('上传 1–3 张人物参考图')).toBeVisible()
  await page.getByLabel('上传参考图').setInputFiles({ name: 'face.png', mimeType: 'image/png', buffer: Buffer.alloc(512, 1) })
  const drawer = page.getByRole('dialog', { name: '新建角色' })
  await expect(drawer.getByRole('button', { name: '移除图 1' }).or(drawer.getByText('图1'))).toBeVisible()
})

test('saving a result as a character opens the form with that image', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/assets/portrait')
  await page.getByRole('button', { name: '保存为角色' }).click()
  await expect(page).toHaveURL(/\/characters$/)
  await expect(page.getByRole('dialog', { name: '新建角色' }).getByText('图1')).toBeVisible()
})

test('English: the character library reads in English', async ({ page }) => {
  await useDemoApi(page, { lang: 'en' })
  await page.goto('/characters')
  await expect(page.getByRole('heading', { name: 'Characters', level: 1 })).toBeVisible()
  await expect(card(page, '阿橘').getByRole('button', { name: 'Use in creation' })).toBeVisible()
  await expect(card(page, '阿橘')).toContainText('2 reference images')
})
