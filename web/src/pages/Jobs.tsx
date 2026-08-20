import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { statusToPhase, WORKFLOW_LABEL_KEY, resolveTab } from '../lib/jobResult'
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
  const [projectId, setProjectId] = useState('')
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.listProjects })
  const qc = useQueryClient()

  const query = useInfiniteQuery({
    queryKey: ['jobs', status, projectId],
    queryFn: ({ pageParam }: { pageParam?: string }) =>
      api.listJobs({ status, cursor: pageParam, limit: 20, projectId: projectId || undefined }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
  })

  // Soft-delete only (jobsvc.Service.Delete's own doc) — same "no confirm
  // dialog, the row just disappears" pattern deleteAsset already uses,
  // since it's reversible at the DB level even without a restore UI yet.
  const deleteJob = useMutation({
    mutationFn: (bizId: string) => api.deleteJob(bizId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['jobs'] }),
  })

  // §07's "掛起置頂 + 呼吸黃條" gap — a suspended job (video.sequence's
  // preview gate, R12) is the one state that genuinely needs the user to
  // come back and act, so it gets pulled to the top of whatever page is
  // currently loaded rather than sitting wherever created_at put it. A
  // stable sort (Array.prototype.sort is stable per spec) keeps every other
  // ordering exactly as the backend returned it.
  const jobs = [...(query.data?.pages.flatMap((p) => p.jobs) ?? [])].sort((a, b) =>
    a.status === 'suspended' && b.status !== 'suspended' ? -1 : b.status === 'suspended' && a.status !== 'suspended' ? 1 : 0,
  )

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
      <div className="mx-auto max-w-5xl px-6 py-8">
        <div className="mb-6 flex flex-wrap items-center justify-between gap-2">
          <h1 className="text-lg font-medium">{t('rail.jobs')}</h1>
          <div className="flex flex-wrap items-center gap-2">
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
            {!!projects.data?.projects.length && (
              <select
                value={projectId}
                onChange={(e) => setProjectId(e.target.value)}
                className="rounded-lg border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-sm text-zinc-300 outline-none focus:border-violet-500"
              >
                <option value="">{t('jobs.allProjects')}</option>
                {projects.data.projects.map((p) => (
                  <option key={p.biz_id} value={p.biz_id}>
                    {p.name}
                  </option>
                ))}
              </select>
            )}
          </div>
        </div>

        {query.isSuccess && jobs.length === 0 && <p className="text-zinc-500">{t('jobs.empty')}</p>}

        <div className="space-y-2">
          {jobs.map((j) => {
            const label = t(WORKFLOW_LABEL_KEY[resolveTab(j.workflow_name)]) || j.workflow_name
            return (
              <Link
                key={j.biz_id}
                to={`/jobs/${j.biz_id}`}
                className={`group flex items-center gap-4 rounded-xl border px-4 py-3 transition ${
                  j.status === 'suspended'
                    ? 'animate-pulse border-amber-500/60 bg-amber-500/5 hover:border-amber-400'
                    : 'border-zinc-800 bg-zinc-900/60 hover:border-zinc-700'
                }`}
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
                {(j.status === 'succeeded' || j.status === 'failed' || j.status === 'cancelled') && (
                  <button
                    type="button"
                    onClick={(e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      deleteJob.mutate(j.biz_id)
                    }}
                    title={t('common.delete')}
                    className="shrink-0 rounded-full p-1 text-zinc-600 opacity-0 transition hover:text-red-400 focus:opacity-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-violet-500 group-hover:opacity-100"
                  >
                    ×
                  </button>
                )}
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
