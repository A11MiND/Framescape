import { test, expect, type Page } from '@playwright/test'
import { useDemoApi } from './demo'

const card = (page: Page, name: string) => page.getByRole('link', { name, exact: true })

test('filters live in the URL and narrow the grid', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/assets')
  await expect(card(page, '午后阳光下的猫咪')).toBeVisible()
  await page.getByRole('combobox', { name: '类型' }).selectOption('video')
  await expect(page).toHaveURL(/type=video/)
  await expect(card(page, '电车缓缓驶入画面')).toBeVisible()
  await expect(card(page, '午后阳光下的猫咪')).toHaveCount(0)
  await page.getByRole('textbox', { name: '搜索提示词' }).fill('咖啡')
  await expect(page).toHaveURL(/q=/)
  await expect(card(page, '海边咖啡馆的露台')).toBeVisible()
  await expect(card(page, '电车缓缓驶入画面')).toHaveCount(0)
  await page.getByRole('textbox', { name: '搜索提示词' }).fill('不存在的内容')
  await expect(page.getByText('没有符合筛选的素材')).toBeVisible()
  await page.getByRole('button', { name: '清除筛选' }).click()
  await expect(card(page, '午后阳光下的猫咪')).toBeVisible()
})

test('selection counts items hidden by a filter, and deleting them can be undone', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/assets')
  await expect(page.getByRole('region', { name: /已选/ })).toHaveCount(0)
  await page.getByRole('button', { name: '选择', exact: true }).click()
  await page.getByRole('button', { name: '选择 电车缓缓驶入画面' }).click()
  await page.getByRole('button', { name: '选择 午后阳光下的猫咪' }).click()
  const bar = page.getByRole('region', { name: '已选 2 个' })
  await expect(bar).toBeVisible()
  await page.getByRole('combobox', { name: '类型' }).selectOption('video')
  await expect(bar).toContainText('另有 1 个已选素材不在当前筛选中')

  await bar.getByRole('button', { name: '删除' }).click()
  const dialog = page.getByRole('alertdialog').or(page.getByRole('dialog'))
  await expect(dialog).toContainText('删除 2 个素材？')
  await expect(dialog).toContainText('其中 1 个不在当前筛选中。')
  await expect(dialog).toContainText('30 天内可以恢复')
  await dialog.getByRole('button', { name: '移到回收站' }).click()
  await expect(page.getByText('已删除 2 个素材，已移到回收站。')).toBeVisible()
  await expect(card(page, '电车缓缓驶入画面')).toHaveCount(0)
  await expect(page.getByRole('tab', { name: /回收站\s*2/ })).toBeVisible()

  await page.getByRole('button', { name: '撤销' }).click()
  await expect(page.getByText('已恢复 2 个素材。')).toBeVisible()
  await expect(page.getByRole('button', { name: '选择 电车缓缓驶入画面' })).toBeVisible()
  await expect(page.getByRole('region', { name: /已选/ })).toHaveCount(0)
})

test('moving selected assets into a project names the target on the cards', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/assets')
  await page.getByRole('button', { name: '选择', exact: true }).click()
  await page.getByRole('button', { name: '选择 午后阳光下的猫咪' }).click()
  await page.getByRole('button', { name: '选择 柠檬树的特写' }).click()
  await page.getByRole('region', { name: '已选 2 个' }).getByRole('button', { name: '归入项目' }).click()
  const dialog = page.getByRole('dialog', { name: '归入项目' })
  await expect(dialog).toContainText('把 2 个素材归入：')
  await dialog.getByRole('combobox', { name: '目标项目' }).selectOption({ label: '日落计划' })
  await dialog.getByRole('button', { name: '归入', exact: true }).click()
  await expect(page.getByText('已把 2 个素材归入项目。')).toBeVisible()
  await page.getByRole('main').getByRole('combobox', { name: '项目', exact: true }).selectOption({ label: '日落计划' })
  await expect(page.getByRole('button', { name: '选择 午后阳光下的猫咪' })).toBeVisible()
  await expect(page.getByRole('button', { name: '选择 柠檬树的特写' })).toBeVisible()
  await expect(page.getByRole('button', { name: '选择 电车缓缓驶入画面' })).toHaveCount(0)
})

test('the trash restores one item or empties for good, stating how many', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/assets')
  for (const name of ['午后阳光下的猫咪', '柠檬树的特写']) {
    await page.getByRole('button', { name: `${name} 的更多操作` }).click()
    await page.getByRole('menuitem', { name: '删除' }).click()
    await page.getByRole('button', { name: '移到回收站' }).click()
    await expect(card(page, name)).toHaveCount(0)
  }
  await page.getByRole('tab', { name: /回收站/ }).click()
  await expect(page).toHaveURL(/tab=trash/)
  await expect(page.getByText('回收站里的素材在删除 30 天后永久删除。')).toBeVisible()
  await page.getByRole('listitem').filter({ hasText: '柠檬树的特写' }).getByRole('button', { name: '恢复' }).click()
  await expect(page.getByText('已恢复 1 个素材。')).toBeVisible()

  await page.getByRole('button', { name: '清空回收站（1）' }).click()
  const dialog = page.getByRole('alertdialog').or(page.getByRole('dialog'))
  await expect(dialog).toContainText('将永久删除回收站里的 1 个素材，删除后无法恢复。')
  await dialog.getByRole('button', { name: '永久删除' }).click()
  await expect(page.getByText('已永久删除 1 个素材。')).toBeVisible()
  await expect(page.getByText('回收站是空的')).toBeVisible()
})

test('uploads show real progress, finish, and refuse what cannot be uploaded', async ({ page }) => {
  await useDemoApi(page)
  let release!: () => void
  const held = new Promise<void>((r) => (release = r))
  let completed = false
  await page.route('**/api/v1/assets/upload-url', (route) =>
    route.fulfill({ json: { biz_id: 'up-1', upload_url: 'http://127.0.0.1:4173/fake-storage/up-1', storage_key: 'image/up-1.png' } }),
  )
  await page.route('**/fake-storage/**', async (route) => {
    await held
    await route.fulfill({ status: 200, body: '' })
  })
  await page.route('**/api/v1/assets/up-1/complete', (route) => {
    completed = true
    return route.fulfill({ json: { biz_id: 'up-1', type: 'image', public_url: '', mime: 'image/png' } })
  })
  await page.goto('/assets')
  await page.getByRole('button', { name: '上传素材' }).first().click()
  const drawer = page.getByRole('dialog', { name: '上传素材' })
  await expect(drawer).toContainText('图片不超过 20 MB')
  await drawer.getByLabel('选择文件').setInputFiles([
    { name: 'sketch.png', mimeType: 'image/png', buffer: Buffer.alloc(2048, 1) },
    { name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('hello') },
  ])
  const queue = drawer.getByRole('region', { name: '上传队列' })
  const sketch = queue.getByRole('listitem').filter({ hasText: 'sketch.png' })
  await expect(sketch.getByText(/上传中 \d+%/)).toBeVisible()
  await expect(sketch.getByRole('button', { name: '取消上传' })).toBeVisible()
  const notes = queue.getByRole('listitem').filter({ hasText: 'notes.txt' })
  await expect(notes.getByRole('alert')).toHaveText('上传失败：只能上传图片、视频或音频')
  await expect(notes.getByRole('button', { name: '重试' })).toHaveCount(0)

  release()
  await expect(sketch.getByText('已完成')).toBeVisible()
  expect(completed).toBe(true)
  await drawer.getByRole('button', { name: '清除已完成' }).click()
  await expect(sketch).toHaveCount(0)
  await expect(notes).toBeVisible()
})

test('English: selection and trash read in English', async ({ page }) => {
  await useDemoApi(page, { lang: 'en' })
  await page.goto('/assets')
  await expect(page.getByRole('heading', { name: 'Library' })).toBeVisible()
  await page.getByRole('button', { name: 'Select', exact: true }).click()
  await page.getByRole('button', { name: 'Select 午后阳光下的猫咪' }).click()
  await page.getByRole('button', { name: 'Select 柠檬树的特写' }).click()
  const bar = page.getByRole('region', { name: '2 selected' })
  await bar.getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('button', { name: 'Move to trash' }).click()
  await expect(page.getByText('2 items moved to the trash.')).toBeVisible()
  await page.getByRole('tab', { name: /Trash/ }).click()
  await expect(page.getByRole('button', { name: 'Empty trash (2)' })).toBeVisible()
})
