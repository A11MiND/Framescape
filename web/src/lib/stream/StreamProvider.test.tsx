import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useAuthStore } from '../authStore'
import { keys } from '../api/keys'
import { StreamProvider } from './StreamProvider'
import { useStream } from './context'

function streamOf(text: string) {
  return new Response(
    new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(text))
        controller.close()
      },
    }),
    { status: 200, headers: { 'Content-Type': 'text/event-stream' } },
  )
}

function Status() {
  const { status } = useStream()
  return <span data-testid="status">{status}</span>
}

describe('StreamProvider', () => {
  it('resumes from the last event id and refreshes affected queries', async () => {
    useAuthStore.setState({ accessToken: 'token-1', refreshToken: 'r' })
    const qc = new QueryClient()
    const invalidate = vi.spyOn(qc, 'invalidateQueries')
    const event = { id: 42, type: 'job.finished', job_id: 'JOB1', payload: {}, created_at: '' }
    const seen: Headers[] = []
    let calls = 0
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init: RequestInit) => {
        seen.push(new Headers(init.headers))
        calls++
        if (calls === 1) return streamOf(`id: 42\nevent: job.finished\ndata: ${JSON.stringify(event)}\n\n`)
        return new Promise<Response>(() => {})
      }),
    )
    render(
      <QueryClientProvider client={qc}>
        <StreamProvider>
          <Status />
        </StreamProvider>
      </QueryClientProvider>,
    )
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: keys.jobs.detail('JOB1') }))
    expect(invalidate).toHaveBeenCalledWith({ queryKey: keys.jobs.all })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: keys.me })
    expect(seen[0].get('Authorization')).toBe('Bearer token-1')
    expect(seen[0].has('Last-Event-ID')).toBe(false)
    await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('offline'))
    await waitFor(() => expect(calls).toBe(2), { timeout: 3000 })
    expect(seen[1].get('Last-Event-ID')).toBe('42')
    vi.unstubAllGlobals()
    useAuthStore.setState({ accessToken: null, refreshToken: null })
  })
})
