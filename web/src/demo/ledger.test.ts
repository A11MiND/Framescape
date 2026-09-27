import { describe, expect, it } from 'vitest'
import { createDemoApi } from './api'

describe('demo billing examples', () => {
  for (const id of ['J005', 'J014'])
    it(`${id} has a consistent ledger and linked task`, () => {
      const api = createDemoApi((f) => f)
      const ledger = api('GET', '/credits/ledger', '', undefined).body as {
        entries: { direction: string; amount: number; remark: { amount: number; shortfall?: number }; job?: { biz_id: string } }[]
      }
      const entries = ledger.entries.filter((e) => e.job?.biz_id === id)
      const held = entries.filter((e) => e.direction === 'hold').reduce((s, e) => s + e.remark.amount, 0)
      const settled = entries.filter((e) => e.direction === 'commit').reduce((s, e) => s - e.amount, 0)
      const released = entries.filter((e) => e.direction === 'refund').reduce((s, e) => s + e.remark.amount, 0)
      const overage = entries.reduce((s, e) => s + (e.remark.shortfall ?? 0), 0)
      expect(released).toBe(Math.max(0, held - settled))
      expect(overage).toBe(Math.max(0, settled - held))
      const job = api('GET', `/jobs/${id}`, '', undefined).body as {
        credits: { reserved: number; settled: number; released: number; overage: number }
      }
      expect(job.credits).toMatchObject({ reserved: held, settled, released, overage })
    })
})
