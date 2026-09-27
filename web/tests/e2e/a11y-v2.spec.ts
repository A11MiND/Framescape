import { expect, test } from '@playwright/test'
import { useDemoApi } from './demo'

const routes = [
  '/create/image', '/create/image-sequence', '/create/video', '/create/video-sequence',
  '/create/comic', '/create/comic/C1/edit', '/create/comic-classic', '/login', '/jobs',
  '/jobs/J005', '/jobs/J012', '/assets', '/assets/tram-1', '/projects', '/projects/P1',
  '/characters', '/presets', '/community', '/credits', '/settings', '/admin/spend',
  '/admin/users', '/create/gpt',
]

type Audit = { unnamed: string[]; missingAlt: number; unlabeledDialogs: number }

function auditDom(): Audit {
  const visible = (element: Element) => {
    const style = getComputedStyle(element)
    return style.display !== 'none' && style.visibility !== 'hidden' && (element as HTMLElement).offsetParent !== null
  }
  const labelFor = (element: Element) => {
    const labelledBy = element.getAttribute('aria-labelledby')
    if (labelledBy) return labelledBy.split(/\s+/).map((id) => document.getElementById(id)?.textContent ?? '').join(' ').trim()
    const aria = element.getAttribute('aria-label')
    if (aria) return aria.trim()
    if (element instanceof HTMLInputElement || element instanceof HTMLSelectElement || element instanceof HTMLTextAreaElement) {
      if (element.labels?.length) return Array.from(element.labels).map((label) => label.textContent ?? '').join(' ').trim()
      return (element.getAttribute('placeholder') ?? '').trim()
    }
    return (element.textContent ?? '').replace(/\s+/g, ' ').trim()
  }
  const unnamed: string[] = []
  document.querySelectorAll('button, a[href], input:not([type="hidden"]), select, textarea, [role="button"], [role="link"]').forEach((element) => {
    if (visible(element) && !labelFor(element)) unnamed.push(element.outerHTML.slice(0, 180))
  })
  const missingAlt = Array.from(document.querySelectorAll('img')).filter((img) => visible(img) && !img.hasAttribute('alt')).length
  const unlabeledDialogs = Array.from(document.querySelectorAll('[role="dialog"]')).filter((dialog) => visible(dialog) && !labelFor(dialog)).length
  return { unnamed, missingAlt, unlabeledDialogs }
}

test.describe('phase 7 accessibility smoke audit', () => {
  for (const lang of ['zh', 'en'] as const) {
    test(`${lang}: visible controls have names and images have alt text`, async ({ page }) => {
      test.setTimeout(120000)
      await useDemoApi(page, { lang, theme: 'light' })
      const failures: Array<{ route: string; audit: Audit }> = []
      for (const route of routes) {
        await page.goto(route)
        await expect(page.locator('main')).toBeVisible()
        const audit = await page.evaluate(auditDom)
        if (audit.unnamed.length || audit.missingAlt || audit.unlabeledDialogs) failures.push({ route, audit })
      }
      expect(failures, JSON.stringify(failures, null, 2)).toEqual([])
    })
  }
})
