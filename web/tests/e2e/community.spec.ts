import { test, expect, type Page } from '@playwright/test'
import { useDemoApi } from './demo'

const work = (page: Page, title: RegExp) => page.getByRole('button', { name: title })
const viewer = (page: Page) => page.getByRole('complementary', { name: '作品详情' })

test('guest root opens the community instead of an empty creation form', async ({ page }) => {
  await useDemoApi(page, { guest: true })
  await page.goto('/')
  await expect(page).toHaveURL(/\/community$/)
  await expect(page.getByRole('heading', { name: '看看灵感，能变成什么。' })).toBeVisible()
  await expect(page.getByRole('button', { name: /查看作品/ }).first()).toBeVisible()
})

test('guests browse works without a sign-in wall and are told to sign in to act', async ({ page }) => {
  await useDemoApi(page, { guest: true })
  await page.goto('/community')
  await expect(page.getByRole('heading', { name: '看看灵感，能变成什么。' })).toBeVisible()
  await work(page, /查看作品：午后阳光下的猫咪/).click()
  const v = viewer(page)
  await expect(v).toContainText('午后阳光下的猫咪')
  await expect(v).toContainText('登录后可以点赞和发布作品。')
  await v.getByRole('button', { name: /301/ }).click()
  await expect(page.getByText('登录后可以点赞和发布作品。').first()).toBeVisible()
  await expect(page.getByRole('region', { name: '连续发布' })).toHaveCount(0)
  await page.getByRole('tab', { name: '我发布的' }).click()
  await expect(page.getByText('登录后查看你发布的作品。')).toBeVisible()
})

test('type filter, likes and the public prompt', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/community')
  await page.getByRole('radio', { name: '视频' }).click()
  await expect(page).toHaveURL(/type=video/)
  await expect(work(page, /查看作品：海边咖啡馆的露台/)).toBeVisible()
  await expect(work(page, /查看作品：午后阳光下的猫咪/)).toHaveCount(0)
  await page.getByRole('radio', { name: '全部' }).click()

  await work(page, /查看作品：午后阳光下的猫咪/).click()
  const v = viewer(page)
  await expect(v).toContainText('原始提示词')
  await expect(v.getByRole('button', { name: '复制提示词' })).toBeVisible()
  const likeBtn = v.getByRole('button', { name: /喜欢/ })
  await expect(likeBtn).toHaveAttribute('aria-pressed', 'false')
  await likeBtn.click()
  await expect(likeBtn).toHaveAttribute('aria-pressed', 'true')
  await expect(likeBtn).toContainText('302')
  await expect(work(page, /查看作品：午后阳光下的猫咪/)).toContainText('302')
  await expect(v).toContainText('只带上公开的提示词，不会使用作者的参考图或角色。')
})

test('create similar carries only the public prompt into creation', async ({ page }) => {
  await useDemoApi(page)
  const jobs: Record<string, unknown>[] = []
  page.on('request', (r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/jobs' && jobs.push(r.postDataJSON()))
  await page.goto('/community')
  await work(page, /查看作品：雪山湖泊全景/).click()
  await viewer(page).getByRole('button', { name: '创作相似作品' }).click()
  await expect(page).toHaveURL(/\/create\/image$/)
  await expect(page.getByRole('combobox', { name: '描述画面' })).toHaveValue('雪山湖泊全景')
})

test('your own published work can be unpublished from the community', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/assets/tram-1')
  await page.getByRole('button', { name: '发布到社区' }).click()
  await page.getByRole('button', { name: '发布', exact: true }).click()
  await expect(page.getByText('已发布到社区').first()).toBeVisible()

  await page.goto('/community?tab=mine')
  await expect(work(page, /查看作品：海边小镇的有轨电车/)).toBeVisible()
  await expect(work(page, /查看作品：午后阳光下的猫咪/)).toHaveCount(0)
  await work(page, /查看作品：海边小镇的有轨电车/).click()
  const v = viewer(page)
  await expect(v).toContainText('这是你发布的作品')
  await expect(v.getByRole('link', { name: '在素材库中查看' })).toHaveAttribute('href', '/assets/tram-1')
  await v.getByRole('button', { name: '取消发布' }).click()
  await expect(page.getByText('已取消发布，作品仍在你的素材库')).toBeVisible()
  await expect(work(page, /查看作品：海边小镇的有轨电车/)).toHaveCount(0)
  await expect(page.getByText('你还没有发布作品')).toBeVisible()
})

test('the publishing streak states its rules and reward caps in times', async ({ page }) => {
  await useDemoApi(page)
  await page.goto('/community')
  await expect(page.getByRole('link', { name: '连续发布 3 天' })).toBeVisible()
  const streak = page.getByRole('region', { name: '连续发布' })
  await expect(streak).toContainText('当前已连续发布 3 天')
  await expect(streak).toContainText('连续 3 天')
  await expect(streak).toContainText('本月已获得 1/4 次')
  await expect(streak).toContainText('不限次数')
  await expect(streak).toContainText('北京时间每天 8:00 换日')
  await expect(streak.getByRole('img', { name: /最近 12 周的发布记录/ })).toBeVisible()
  await streak.getByRole('button', { name: /连续发布/ }).click()
  await expect(streak).not.toContainText('里程碑奖励')
})

test('English: the community reads in English', async ({ page }) => {
  await useDemoApi(page, { lang: 'en' })
  await page.goto('/community')
  await expect(page.getByRole('heading', { name: 'Discover', level: 1 })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'My published' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Publishing streak' })).toContainText('No monthly limit')
})
