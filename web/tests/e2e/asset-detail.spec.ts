import { test, expect } from '@playwright/test'
import { useDemoApi } from './demo'

test('a generated asset shows its prompt, parameters, source task and project', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/assets/cat')
  await expect(page.getByRole('link', { name: '返回素材库' })).toBeVisible()
  await expect(page.getByText('午后阳光下的猫咪').first()).toBeVisible()
  await expect(page.getByText('1536×1024').first()).toBeVisible()
  await expect(page.getByRole('button', { name: '复制提示词' })).toBeVisible()
  await expect(page.getByText('来源任务')).toBeVisible()

  const project = page.getByRole('combobox', { name: '所属项目' })
  await expect(project).toHaveValue('P1')
  await project.selectOption({ label: '日落计划' })
  await expect(page.getByText('已更新所属项目')).toBeVisible()
  await expect(project).toHaveValue('P2')
})

test('publishing states what becomes public and what stays private', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/assets/cat')
  await expect(page.getByText('未发布')).toBeVisible()
  await page.getByRole('button', { name: '发布到社区' }).click()
  const dialog = page.getByRole('alertdialog').or(page.getByRole('dialog'))
  await expect(dialog).toContainText('发布后所有人都能在社区看到')
  await expect(dialog).toContainText('它的提示词')
  await expect(dialog).toContainText('模型、种子等生成参数，以及你用到的参考图、角色图和资料，都不会公开。')
  await dialog.getByRole('button', { name: '发布', exact: true }).click()
  await expect(page.getByText('已发布到社区').first()).toBeVisible()
  await expect(page.getByRole('button', { name: '取消发布' })).toBeVisible()
  await page.getByRole('button', { name: '取消发布' }).click()
  await expect(page.getByText('已取消发布')).toBeVisible()
  await expect(page.getByRole('button', { name: '发布到社区' })).toBeVisible()
})

test('an uploaded asset says it has no generation parameters', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/assets/portrait')
  await expect(page.getByText('上传素材，无生成参数')).toBeVisible()
  await expect(page.getByRole('button', { name: '复制提示词' })).toHaveCount(0)
  await expect(page.getByText('没有关联的任务')).toBeVisible()
})

test('a file that cannot be read and an unknown asset each explain themselves', async ({ page }) => {
  await useDemoApi(page)
  await page.route('**/src/demo/assets/lemon.jpg', (route) => route.fulfill({ status: 404, body: '' }))
  await page.goto('/assets/lemon')
  await expect(page.getByText('文件已失效')).toBeVisible()
  await expect(page.getByRole('link', { name: '返回素材库' })).toBeVisible()

  await page.goto('/assets/nope')
  await expect(page.getByText('找不到这个素材')).toBeVisible()
})

test('deleting from the detail page moves the asset to the trash', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/assets/cat')
  await page.getByRole('button', { name: '更多操作' }).click()
  await page.getByRole('menuitem', { name: '删除素材' }).click()
  const dialog = page.getByRole('alertdialog').or(page.getByRole('dialog'))
  await expect(dialog).toContainText('30 天内可以恢复')
  await dialog.getByRole('button', { name: '移到回收站' }).click()
  await expect(page).toHaveURL(/\/assets(\?|$)/)
  await expect(page.getByRole('link', { name: '午后阳光下的猫咪', exact: true })).toHaveCount(0)
})
