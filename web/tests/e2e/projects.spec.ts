import { test, expect } from '@playwright/test'
import { useDemoApi } from './demo'

test('projects show real counts; creating and editing one updates its card', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/projects')
  const coast = page.getByRole('listitem').filter({ hasText: '海岸之城' })
  await expect(coast).toContainText('24 素材 · 8 任务 · 2 角色')

  await page.getByRole('button', { name: '新建项目' }).first().click()
  let dialog = page.getByRole('dialog', { name: '新建项目' })
  const create = dialog.getByRole('button', { name: '创建' })
  await expect(create).toBeDisabled()
  await dialog.getByRole('textbox', { name: '名称' }).fill('城市夜游')
  await create.click()
  await expect(page.getByText('项目已创建')).toBeVisible()
  const night = page.getByRole('listitem').filter({ hasText: '城市夜游' })
  await expect(night).toContainText('还没有素材')

  await night.getByRole('button', { name: '城市夜游 的更多操作' }).click()
  await page.getByRole('menuitem', { name: '编辑' }).click()
  dialog = page.getByRole('dialog', { name: '编辑项目' })
  await expect(dialog.getByRole('textbox', { name: '名称' })).toHaveValue('城市夜游')
  await dialog.getByRole('textbox', { name: /描述/ }).fill('夜景素材合集')
  await dialog.getByRole('button', { name: '保存' }).click()
  await expect(page.getByText('项目已保存')).toBeVisible()
  await expect(night).toContainText('夜景素材合集')

  await page.getByRole('textbox', { name: /搜索/ }).fill('不存在')
  await expect(page.getByText('没有符合搜索的项目')).toBeVisible()
})

test('deleting a project lists what it holds and that all of it is kept', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/projects')
  await page.getByRole('button', { name: '海岸之城 的更多操作' }).click()
  await page.getByRole('menuitem', { name: '删除项目' }).click()
  const dialog = page.getByRole('alertdialog').or(page.getByRole('dialog'))
  await expect(dialog).toContainText('删除项目「海岸之城」？')
  await expect(dialog).toContainText('24 个素材')
  await expect(dialog).toContainText('8 个任务')
  await expect(dialog).toContainText('2 个角色')
  await dialog.getByRole('button', { name: '删除项目' }).click()
  await expect(page.getByText('项目已删除，24 个素材、8 个任务、2 个角色已变为未归属。')).toBeVisible()
  await expect(page.getByRole('listitem').filter({ hasText: '海岸之城' })).toHaveCount(0)
})

test('a project page splits its assets, tasks and characters into tabs', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/projects/P1')
  await expect(page.getByRole('heading', { name: '海岸之城' })).toBeVisible()
  await expect(page.getByRole('tab', { name: /素材\s*24/ })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('link', { name: '在素材库中查看' })).toHaveAttribute('href', '/assets?project=P1')

  await page.getByRole('tab', { name: /任务\s*8/ }).click()
  await expect(page).toHaveURL(/tab=tasks/)
  await expect(page.getByRole('link', { name: '在任务中心查看' })).toHaveAttribute('href', '/jobs?project=P1')

  await page.getByRole('tab', { name: /角色\s*2/ }).click()
  await expect(page).toHaveURL(/tab=characters/)

  await page.goto('/projects/P404')
  await expect(page.getByText('找不到这个项目')).toBeVisible()
})
