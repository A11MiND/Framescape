import { useQuery, useInfiniteQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, type CreditLedgerEntry } from '../lib/api'
import AppShell from '../components/AppShell'
import AnimatedNumber from '../components/AnimatedNumber'

// F1.3's balance/ledger page — `held` never had an out beyond the raw
// number that used to sit unused in credit_accounts (the blueprint's own
// gap note); this is its first display.
export default function Credits() {
  const { t } = useTranslation()
  const balance = useQuery({ queryKey: ['credits', 'balance'], queryFn: api.creditsBalance })
  const ledger = useInfiniteQuery({
    queryKey: ['credits', 'ledger'],
    queryFn: ({ pageParam }: { pageParam?: string }) => api.creditsLedger({ cursor: pageParam, limit: 30 }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
  })

  const entries = ledger.data?.pages.flatMap((p) => p.entries) ?? []

  return (
    <AppShell>
      <div className="mx-auto max-w-3xl px-6 py-8">
        <h1 className="mb-6 text-lg font-medium">{t('credits.title')}</h1>

        <div className="mb-8 grid grid-cols-2 gap-3">
          <div className="rounded-xl border border-zinc-800 bg-zinc-900/60 p-5">
            <p className="text-xs uppercase tracking-wide text-zinc-500">{t('credits.balance')}</p>
            <p className="mt-1 font-mono text-2xl text-violet-300">
              <AnimatedNumber value={balance.data?.balance ?? 0} />
            </p>
          </div>
          <div className="rounded-xl border border-zinc-800 bg-zinc-900/60 p-5">
            <p className="text-xs uppercase tracking-wide text-zinc-500">{t('credits.held')}</p>
            <p className="mt-1 font-mono text-2xl text-zinc-300">
              <AnimatedNumber value={balance.data?.held ?? 0} />
            </p>
          </div>
        </div>

        <p className="mb-3 text-xs uppercase tracking-wide text-zinc-500">{t('credits.ledger')}</p>
        <div className="overflow-hidden rounded-xl border border-zinc-800">
          <table className="w-full text-sm">
            <tbody>
              {entries.map((e, i) => (
                <LedgerRow key={i} entry={e} />
              ))}
            </tbody>
          </table>
          {ledger.isSuccess && entries.length === 0 && (
            <p className="p-4 text-center text-sm text-zinc-500">{t('credits.empty')}</p>
          )}
        </div>

        {ledger.hasNextPage && (
          <button
            onClick={() => ledger.fetchNextPage()}
            disabled={ledger.isFetchingNextPage}
            className="mt-4 w-full rounded-lg border border-zinc-800 py-2 text-sm text-zinc-400 transition hover:border-zinc-700 hover:text-zinc-200 disabled:opacity-50"
          >
            {ledger.isFetchingNextPage ? t('common.loading') : t('credits.loadMore')}
          </button>
        )}
      </div>
    </AppShell>
  )
}

function LedgerRow({ entry }: { entry: CreditLedgerEntry }) {
  const { t, i18n } = useTranslation()
  // hold/refund record amount=0 by design (creditsvc's own doc: they're
  // pure balance<->held reallocations) — the real magnitude lives in
  // `remark` instead, which is exactly what's shown for those two
  // directions so this row is never just "0" with no useful information.
  const showAmount = entry.direction === 'recharge' || entry.direction === 'commit'
  return (
    <tr className="border-b border-zinc-800 last:border-0">
      <td className="px-4 py-2.5 text-zinc-400">{t(`credits.direction.${entry.direction}`, entry.direction)}</td>
      <td className="px-4 py-2.5 text-zinc-500">{entry.remark}</td>
      <td className="px-4 py-2.5 text-right font-mono text-xs text-zinc-600">
        {new Date(entry.created_at).toLocaleString(i18n.language === 'en' ? 'en-US' : 'zh-CN', {
          month: '2-digit',
          day: '2-digit',
          hour: '2-digit',
          minute: '2-digit',
        })}
      </td>
      <td className="px-4 py-2.5 text-right font-mono">
        {showAmount && (
          <span className={entry.amount > 0 ? 'text-emerald-400' : 'text-zinc-300'}>
            {entry.amount > 0 ? '+' : ''}
            {entry.amount}
          </span>
        )}
      </td>
    </tr>
  )
}
