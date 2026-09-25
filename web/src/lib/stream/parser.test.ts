import { describe, expect, it } from 'vitest'
import { backoffDelay, parseFrames, toStreamEvent } from './parser'

describe('parseFrames', () => {
  it('parses complete frames and keeps the partial rest', () => {
    const { frames, rest } = parseFrames('id: 7\nevent: job.status\ndata: {"id":7}\n\n: heartbeat\n\nid: 8\nevent: job.fin')
    expect(frames).toEqual([
      { id: '7', event: 'job.status', data: '{"id":7}' },
      { id: undefined, event: 'message', data: '' },
    ])
    expect(rest).toBe('id: 8\nevent: job.fin')
  })

  it('joins multi-line data and accepts CRLF', () => {
    const { frames } = parseFrames('data: a\r\ndata: b\r\n\r\n')
    expect(frames[0].data).toBe('a\nb')
  })
})

describe('toStreamEvent', () => {
  it('reads events and ignores heartbeats and junk', () => {
    expect(toStreamEvent({ event: 'x', data: '{"id":3,"type":"job.status","job_id":"J","payload":{},"created_at":""}' })?.id).toBe(3)
    expect(toStreamEvent({ event: 'message', data: '' })).toBeNull()
    expect(toStreamEvent({ event: 'x', data: 'not json' })).toBeNull()
  })
})

describe('backoffDelay', () => {
  it('doubles from 1s and caps at 30s', () => {
    expect(backoffDelay(0, () => 0)).toBe(1000)
    expect(backoffDelay(3, () => 0)).toBe(8000)
    expect(backoffDelay(10, () => 0)).toBe(30000)
    expect(backoffDelay(0, () => 1)).toBe(700)
  })
})
