import { API_BASE } from '../lib/api/client'
import { useAuthStore } from '../lib/authStore'
import { createDemoApi } from './api'

const files = import.meta.glob('./assets/*.{jpg,mp4}', { eager: true, query: '?url', import: 'default' }) as Record<string, string>
const img = (file: string) => files[`./assets/${file}`] ?? ''

/** Serves the API from sample data in the browser (VITE_DEMO=1 only). */
export function installDemo() {
  const handle = createDemoApi(img)
  const uploads = new Map<string, { biz_id: string; public_url: string; mime: string; type: string; width?: number; height?: number }>()
  const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  const realFetch = window.fetch.bind(window)
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, window.location.href)
    // Local review supports real image files without sending them to storage.
    if (url.origin === window.location.origin && url.pathname.startsWith('/__demo-upload/') && init?.body instanceof Blob) {
      const id = url.pathname.split('/').pop()!
      uploads.set(id, { biz_id: id, public_url: URL.createObjectURL(init.body), mime: init.body.type, type: 'image' })
      return new Response(null, { status: 200 })
    }
    const base = new URL(API_BASE, window.location.href)
    if (url.origin !== base.origin || !url.pathname.startsWith(base.pathname)) return realFetch(input, init)
    const path = url.pathname.slice(base.pathname.length)
    const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
    if (path === '/assets/upload-url' && init?.method === 'POST') {
      const id = `upload-${crypto.randomUUID()}`
      return json({ biz_id: id, upload_url: `${window.location.origin}/__demo-upload/${id}`, storage_key: id })
    }
    const uploaded = path.match(/^\/assets\/([^/]+)(\/complete)?$/)
    if (uploaded && uploads.has(uploaded[1])) {
      const asset = uploads.get(uploaded[1])!
      if (uploaded[2] && body) Object.assign(asset, { width: body.width, height: body.height })
      return json(asset)
    }
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
