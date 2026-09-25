import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { ArrowRight, ImageIcon } from 'lucide-react'
import { Card, ElapsedTime, EmptyState, IndeterminateProgress, Skeleton, StatusPill } from '../../ui'
import { jobsApi } from '../../lib/api/jobs'
import { keys } from '../../lib/api/keys'
import { failureText } from '../../lib/errorText'
import { useStream } from '../../lib/stream/context'
import { useAuthStore } from '../../lib/authStore'
import { ResultViewer } from '../tasks/detail/ResultViewer'
import { ACTIVE, resultAssets } from '../tasks/detail/model'

function CurrentJob({ bizId }: { bizId: string }) {
  const { t } = useTranslation('create')
  const stream = useStream()
  const job = useQuery({
    queryKey: keys.jobs.detail(bizId),
    queryFn: () => jobsApi.get(bizId),
    refetchInterval: (q) => (stream.status !== 'open' && q.state.data && ACTIVE.includes(q.state.data.status) ? 5000 : false),
  })
  if (!job.data) return <Skeleton className="aspect-[3/2] w-full" />
  const j = job.data
  const assets = resultAssets(j)
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <StatusPill status={j.status} />
        <Link to={`/jobs/${bizId}`} className="inline-flex items-center gap-1 text-caption font-medium text-primary-text hover:underline">
          {t('result.viewTask')}
          <ArrowRight aria-hidden className="size-3.5" />
        </Link>
      </div>
      {ACTIVE.includes(j.status) && j.status !== 'awaiting_review' && (
        <Card padding="lg" className="flex flex-col gap-3">
          <h3 className="text-section font-semibold text-fg">{t('result.running')}</h3>
          <IndeterminateProgress label={t('result.running')} />
          <ElapsedTime start={j.started_at ?? j.created_at} className="text-caption text-fg-muted" />
        </Card>
      )}
      {(j.status === 'failed' || j.status === 'partial') && (
        <p role="alert" className="rounded-card border border-danger bg-danger-soft p-3 text-body text-danger-fg">
          {failureText(t, j.error_code || j.nodes.find((n) => n.status === 'failed')?.error_code)}
        </p>
      )}
      {assets.length > 0 && <ResultViewer assetIds={assets} showTitle={false} layout="grid" />}
    </div>
  )
}

function RecentWork() {
  const { t } = useTranslation('create')
  const signedIn = useAuthStore((s) => Boolean(s.accessToken))
  const recent = useQuery({ queryKey: keys.jobs.list({ bucket: 'succeeded', limit: 9 }), queryFn: () => jobsApi.list({ bucket: 'succeeded', limit: 9 }), enabled: signedIn })
  const jobs = (recent.data?.jobs ?? []).filter((j) => j.cover_url)
  if (!jobs.length) return null
  return (
    <section aria-labelledby="recent-title" className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <h2 id="recent-title" className="text-body font-semibold text-fg">
          {t('result.recent')}
        </h2>
        <Link to="/jobs?bucket=succeeded" className="inline-flex items-center gap-1 text-caption text-fg-muted hover:text-fg">
          {t('result.viewAll')}
          <ArrowRight aria-hidden className="size-3.5" />
        </Link>
      </div>
      <ul className="flex gap-2 overflow-x-auto pb-1">
        {jobs.map((j) => (
          <li key={j.biz_id} className="shrink-0">
            <Link to={`/jobs/${j.biz_id}`} aria-label={j.title} className="block size-20 overflow-hidden rounded-thumb bg-surface-2">
              <img src={j.cover_url} alt="" className="size-full object-cover" />
            </Link>
          </li>
        ))}
      </ul>
    </section>
  )
}

/** The workspace's result side: the task just submitted, or an empty state, and recent work. */
export function ResultPane({ jobId, empty, children }: { jobId?: string | null; empty?: ReactNode; children?: ReactNode }) {
  const { t } = useTranslation('create')
  return (
    <section aria-labelledby="result-title" className="flex min-w-0 flex-col gap-5">
      <h2 id="result-title" className="text-section font-semibold text-fg">
        {t('result.title')}
      </h2>
      {children ??
        (jobId ? (
          <CurrentJob bizId={jobId} />
        ) : (
          (empty ?? (
            <Card padding="none">
              <EmptyState icon={<ImageIcon className="size-7" />} title={t('result.emptyTitle')} body={t('result.emptyBody')} />
            </Card>
          ))
        ))}
      <RecentWork />
    </section>
  )
}
