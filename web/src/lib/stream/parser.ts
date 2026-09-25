// Server-sent events wire format over fetch (EventSource cannot send the
// Authorization header).

export interface SSEFrame {
  id?: string
  event: string
  data: string
}

/** Splits buffered text into complete frames; returns the unconsumed rest. */
export function parseFrames(buffer: string): { frames: SSEFrame[]; rest: string } {
  const frames: SSEFrame[] = []
  const text = buffer.replace(/\r\n/g, '\n')
  let start = 0
  for (;;) {
    const end = text.indexOf('\n\n', start)
    if (end === -1) break
    const block = text.slice(start, end)
    start = end + 2
    let id: string | undefined
    let event = 'message'
    const data: string[] = []
    for (const line of block.split('\n')) {
      if (line.startsWith(':')) continue
      const colon = line.indexOf(':')
      const field = colon === -1 ? line : line.slice(0, colon)
      let value = colon === -1 ? '' : line.slice(colon + 1)
      if (value.startsWith(' ')) value = value.slice(1)
      if (field === 'id') id = value
      else if (field === 'event') event = value
      else if (field === 'data') data.push(value)
    }
    frames.push({ id, event, data: data.join('\n') })
  }
  return { frames, rest: text.slice(start) }
}

/** An event of GET /api/v1/stream. */
export interface StreamEvent {
  id: number
  type: string
  job_id: string
  payload: Record<string, unknown>
  created_at: string
}

export function toStreamEvent(frame: SSEFrame): StreamEvent | null {
  if (!frame.data) return null
  try {
    const parsed = JSON.parse(frame.data) as StreamEvent
    return typeof parsed.id === 'number' ? parsed : null
  } catch {
    return null
  }
}

/** Reconnect delay: 1s doubling to 30s, with up to 30% jitter. */
export function backoffDelay(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(30_000, 1000 * 2 ** Math.max(0, attempt))
  return Math.round(base * (1 - 0.3 * random()))
}
