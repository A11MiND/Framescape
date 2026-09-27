import { test, expect } from '@playwright/test'
import { resolve } from 'node:path'
import { useDemoApi } from './demo'

const routes = [
  ['p01', '/create/image'],
  ['p02', '/create/image-sequence'],
  ['p03', '/create/video'],
  ['p04', '/create/video-sequence'],
  ['p05', '/create/comic'],
  ['p06', '/create/comic/C1/edit'],
  ['p07', '/create/comic-classic'],
  ['p08', '/login'],
  ['p09', '/jobs'],
  ['p10', '/jobs/J005'],
  ['p11', '/jobs/J012'],
  ['p12', '/assets'],
  ['p13', '/assets/tram-1'],
  ['p14', '/projects'],
  ['p14-detail', '/projects/P1'],
  ['p15', '/characters'],
  ['p16', '/presets'],
  ['p17', '/community'],
  ['p18', '/credits'],
  ['p19', '/settings'],
  ['p20', '/admin/spend'],
  ['p21', '/admin/users'],
  ['p23', '/create/gpt'],
]
test.describe.configure({ mode: 'parallel' })
for (const theme of ['light', 'dark'] as const)
  for (const width of [375, 390, 768, 1024, 1440])
    for (const [id, url] of routes) {
      test(`${id}: ${theme}, ${width}px`, async ({ page }) => {
        test.setTimeout(30000)
        const errors: string[] = []
        page.on('pageerror', (e) => errors.push(e.message))
        page.on('console', (m) => {
          if (m.type() === 'error') errors.push(m.text())
        })
        await useDemoApi(page, { theme })
        await page.setViewportSize({ width, height: 960 })
        await page.goto(url)
        await expect(page.locator('main')).toBeVisible()
        if (id === 'p06') await expect(page.getByRole('region', { name: '漫画编辑区' })).toBeVisible()
        else await expect(page.locator('main').getByRole('heading').first()).toBeVisible()
        await page.evaluate(() => document.fonts.ready)
        await expect(page.locator('vite-error-overlay')).toHaveCount(0)
        expect(errors).toEqual([])
        await expect(page.getByText('出了点问题', { exact: true })).toHaveCount(0)
        const overflow = await page.evaluate(() => ({ root: document.documentElement.scrollWidth - window.innerWidth, main: (() => { const m = document.querySelector('main')!; return m.scrollWidth - m.clientWidth })() }))
        expect(overflow, `${id} at ${width}px`).toEqual({ root: 0, main: 0 })
        await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
        if (process.env.CAPTURE_REVIEW && ((width === 390 && theme === 'light') || (width === 1440 && theme === 'dark'))) {
          await import('node:fs/promises').then(({ mkdir }) => mkdir('/tmp/framescape-phase6-review/screenshots', { recursive: true }))
          await page.screenshot({
            path: resolve('/tmp/framescape-phase6-review/screenshots', `${id}-${theme}-${width}.png`),
            fullPage: true,
          })
        }
      })
    }
for (const lang of ['zh', 'en'] as const)
  test(`${lang}: new pages and keyboard modal focus at mobile width`, async ({ page }) => {
    await useDemoApi(page, { lang, theme: 'dark' })
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/admin/users')
    await page.getByRole('button', { name: lang === 'zh' ? '新建账号' : 'Create account', exact: true }).click()
    const d = page.getByRole('dialog')
    await expect(d).toBeVisible()
    await page.keyboard.press('Tab')
    expect(await d.evaluate((el) => el.contains(document.activeElement))).toBe(true)
    await page.keyboard.press('Escape')
    await expect(d).toHaveCount(0)
    await expect(page.getByRole('button', { name: lang === 'zh' ? '新建账号' : 'Create account', exact: true })).toBeFocused()
  })
