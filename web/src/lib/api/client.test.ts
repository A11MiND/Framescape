import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, request } from './client'
import { useAuthStore } from '../authStore'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

describe('request on a 401', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('refreshes and replays when the session expired', async () => {
    useAuthStore.getState().setTokens('old', 'refresh')
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json(401, { code: 'unauthorized', message: 'expired' }))
      .mockResolvedValueOnce(json(200, { access_token: 'new', refresh_token: 'refresh2' }))
      .mockResolvedValueOnce(json(200, { ok: true }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(request('GET', '/me')).resolves.toEqual({ ok: true })
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(useAuthStore.getState().accessToken).toBe('new')
  })

  it('treats a wrong current password as an ordinary error and keeps the session', async () => {
    useAuthStore.getState().setTokens('token', 'refresh')
    const fetchMock = vi.fn().mockResolvedValueOnce(json(401, { code: 'invalid_credentials', message: 'current password incorrect' }))
    vi.stubGlobal('fetch', fetchMock)
    const err = await request('PATCH', '/me/password', {}).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).code).toBe('invalid_credentials')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(useAuthStore.getState().accessToken).toBe('token')
  })
})
