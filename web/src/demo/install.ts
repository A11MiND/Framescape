import { API_BASE } from '../lib/api/client'
import { useAuthStore } from '../lib/authStore'
import { createDemoApi } from './api'

const images = import.meta.glob('./assets/*.jpg', { eager: true, query: '?url', import: 'default' }) as Record<string, string>
const img = (name: string) => images[`./assets/${name}.jpg`] ?? ''

/** Serves the API from sample data in the browser (VITE_DEMO=1 only). */
export function installDemo() {
  const handle = createDemoApi(img)
  const realFetch = window.fetch.bind(window)
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, window.location.href)
    const base = new URL(API_BASE, window.location.href)
    if (url.origin !== base.origin || !url.pathname.startsWith(base.pathname)) return realFetch(input, init)
    const path = url.pathname.slice(base.pathname.length)
    const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
    const res = handle((init?.method ?? 'GET').toUpperCase(), path, url.search, body)
    if (res.hang) {
      return new Response(new ReadableStream({ start() {} }), { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
    }
    return new Response(res.status === 204 ? null : JSON.stringify(res.body ?? {}), {
      status: res.status,
      headers: { 'Content-Type': 'application/json' },
    })
  }
  if (!useAuthStore.getState().accessToken) useAuthStore.setState({ accessToken: 'demo', refreshToken: 'demo' })
}
