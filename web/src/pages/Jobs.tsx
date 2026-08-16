import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useInfiniteQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { statusToPhase, WORKFLOW_LABEL_KEY, type Tab } from '../lib/jobResult'
import AppShell from '../components/AppShell'
import PhaseBadge from '../components/PhaseBadge'

const FILTERS = [
  { value: '', labelKey: 'jobs.filter.all' },
  { value: 'running', labelKey: 'jobs.filter.running' },
  { value: 'succeeded', labelKey: 'jobs.filter.succeeded' },
  { value: 'failed', labelKey: 'jobs.filter.failed' },
  { value: 'cancelled', labelKey: 'jobs.filter.cancelled' },
]

// F7.1's job list — every job the user has ever submitted, not just the one
// Studio happens to have inline results for in this browser tab. The
// backing endpoint (GET /jobs) didn't exist before batch 2; this is its
// first consumer.
export default function Jobs() {
  const { t, i18n } = useTranslation()
  const [status, setStatus] = useState('')

  const query = useInfiniteQuery({
    queryKey: ['jobs', status],
    queryFn: ({ pageParam }: { pageParam?: string }) => api.listJobs({ status, cursor: pageParam, limit: 20 }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
  })

  const jobs = query.data?.pages.flatMap((p) => p.jobs) ?? []

  function formatTime(iso: string) {
    return new Date(iso).toLocaleString(i18n.language === 'en' ? 'en-US' : 'zh-CN', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  }

  return (
    <AppShell>
      <div className="mx-auto max-w-4xl px-6 py-8">
        <div className="mb-6 flex items-center justify-between">
          <h1 className="text-lg font-medium">{t('rail.jobs')}</h1>
          <div className="flex gap-1">
            {FILTERS.map((f) => (
              <button
                key={f.value}
                onClick={() => setStatus(f.value)}
                className={`rounded-lg px-3 py-1.5 text-sm transition ${
                  status === f.value ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-400 hover:bg-zinc-900'
                }`}
              >
                {t(f.labelKey)}
              </button>
            ))}
          </div>
        </div>

        {query.isSuccess && jobs.length === 0 && <p className="text-zinc-500">{t('jobs.empty')}</p>}

        <div className="space-y-2">
          {jobs.map((j) => {
            const label = t(WORKFLOW_LABEL_KEY[j.workflow_name as Tab]) || j.workflow_name
            return (
              <Link
                key={j.biz_id}
                to={`/jobs/${j.biz_id}`}
                className="flex items-center gap-4 rounded-xl border border-zinc-800 bg-zinc-900/60 px-4 py-3 transition hover:border-zinc-700"
              >
                <PhaseBadge phase={statusToPhase(j.status)} />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm text-zinc-100">
                    {!!j.retry_of_job_id && (
                      <span title={t('jobs.retryIndicator')} className="mr-1 text-violet-400">
                        ↻
                      </span>
                    )}
                    {j.title || label}
                  </p>
                  <p className="text-xs text-zinc-500">
                    {label}
                    {j.node_total > 0 && ` · ${t('jobs.nodesDone', { done: j.node_done, total: j.node_total })}`}
                    {' · '}
                    {formatTime(j.created_at)}
                  </p>
                </div>
                <div className="shrink-0 text-right font-mono text-xs text-zinc-500">
                  <p>
                    <span className="text-violet-400">✦</span> {j.credit_settled || j.credit_held}
                  </p>
                  {j.credit_held > j.credit_settled && j.status !== 'succeeded' && j.status !== 'failed' && (
                    <p className="text-zinc-600">{t('jobs.held', { count: j.credit_held - j.credit_settled })}</p>
                  )}
                </div>
              </Link>
            )
          })}
        </div>

        {query.hasNextPage && (
          <button
            onClick={() => query.fetchNextPage()}
            disabled={query.isFetchingNextPage}
            className="mt-4 w-full rounded-lg border border-zinc-800 py-2 text-sm text-zinc-400 transition hover:border-zinc-700 hover:text-zinc-200 disabled:opacity-50"
          >
            {query.isFetchingNextPage ? t('common.loading') : t('credits.loadMore')}
          </button>
        )}
      </div>
    </AppShell>
  )
}
