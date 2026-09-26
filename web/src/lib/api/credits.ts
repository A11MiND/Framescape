import { query, request } from './client'

export interface LedgerJob {
  biz_id: string
  title: string
  workflow_name: string
  cover_url?: string
}

export interface LedgerEntry {
  direction: 'hold' | 'commit' | 'refund' | 'recharge' | string
  amount: number
  balance_after: number
  held_after: number
  ref_type: string
  ref_id: string
  remark: { kind: string; amount: number; workflow: string; cost_yuan: number; text: string; node?: string; shortfall?: number }
  created_at: string
  job: LedgerJob | null
}

export const creditsApi = {
  ledger: (cursor?: string) => request<{ entries: LedgerEntry[]; next_cursor?: string }>('GET', `/credits/ledger${query({ cursor, limit: 30 })}`),
  topup: () => request<{ balance: number; held: number; credited: number }>('POST', '/credits/topup'),
}
