import type { Page } from '@playwright/test'
import { createDemoApi } from '../../src/demo/api'

/** Routes the API to the demo sample data (signed in as the demo admin). */
export async function useDemoApi(page: Page, opts: { lang?: 'zh' | 'en'; theme?: 'light' | 'dark' } = {}) {
  const handle = createDemoApi((name) => `/src/demo/assets/${name}.jpg`)
  await page.addInitScript(({ lang, theme }) => {
    localStorage.setItem('aigc.auth', JSON.stringify({ accessToken: 'demo', refreshToken: 'demo' }))
    localStorage.setItem('aigc.lang', lang)
    localStorage.setItem('aigc.theme', theme)
  }, { lang: opts.lang ?? 'zh', theme: opts.theme ?? 'light' })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const res = handle(req.method(), url.pathname.split('/api/v1')[1], url.search, req.postDataJSON?.() ?? undefined)
    if (res.hang) return
    await route.fulfill({ status: res.status, json: res.status === 204 ? undefined : res.body })
  })
}
