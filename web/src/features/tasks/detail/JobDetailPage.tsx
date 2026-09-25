import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, ChevronDown, Copy, PencilLine, RotateCcw, WifiOff } from 'lucide-react'
import {
  Button,
  Card,
  ConfirmDialog,
  Dialog,
  ElapsedTime,
  ErrorState,
  Field,
  IconButton,
  IndeterminateProgress,
  Input,
  LastSync,
  Skeleton,
  StatusPill,
  buttonClasses,
} from '../../../ui'
import { jobsApi, type JobDetail } from '../../../lib/api/jobs'
import { projectsApi } from '../../../lib/api/projects'
import { keys } from '../../../lib/api/keys'
import { ApiError } from '../../../lib/api/client'
import { errorText, failureText } from '../../../lib/errorText'
import { formatClock, formatDateTime, formatDuration, formatNumber } from '../../../lib/format'
import { useStream } from '../../../lib/stream/context'
import { useToast } from '../../../components/Toast'
import type { JobResponse } from '../../../lib/api'
import { resolveTab } from '../../../lib/jobResult'
import { suggestActions, type SuggestedAction } from '../../../lib/suggestions'
import { ACTIVE, TERMINAL, nodeLabel, paramRows, resultAssets } from './model'
import { ResultViewer } from './ResultViewer'
import { ReviewPanel } from './ReviewPanel'

const RETRYABLE: Record<string, string> = { 'video.single': 'gen' }

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-3 text-body">
      <dt className="text-fg-muted">{label}</dt>
      <dd className="text-right text-fg tabular-nums">{value}</dd>
    </div>
  )
}

function CreditsCard({ job }: { job: JobDetail }) {
  const { t, i18n } = useTranslation('job')
  const c = job.credits
  const v = (x: number | null) => (x === null ? t('ui:unknownValue') : formatNumber(x, i18n.language))
  return (
    <Card padding="md">
      <h2 className="mb-3 text-section font-semibold text-fg">{t('credits.title')}</h2>
      <dl className="flex flex-col gap-2">
        <Row label={t('credits.reserved')} value={v(c.reserved)} />
        <Row label={t('credits.settled')} value={v(c.settled)} />
        {c.overage !== null && c.overage > 0 && <Row label={t('credits.overage')} value={v(c.overage)} />}
        <Row label={t('credits.released')} value={v(c.released)} />
        <Row label={t('credits.frozen')} value={v(c.frozen)} />
      </dl>
      <p className="mt-3 text-caption text-fg-muted">{t('credits.note')}</p>
    </Card>
  )
}

function InfoCard({ job }: { job: JobDetail }) {
  const { t } = useTranslation('job')
  const toast = useToast()
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list, enabled: Boolean(job.project_id) })
  const project = projects.data?.projects.find((p) => p.biz_id === job.project_id)
  const shots = Array.isArray(job.spec.shots) ? (job.spec.shots as string[]) : []
  const text = typeof job.spec.text === 'string' ? (job.spec.text as string) : ''
  const params = paramRows(t, job)
  const copy = async (s: string) => {
    try {
      await navigator.clipboard.writeText(s)
      toast(t('done.copied'))
    } catch {
      // clipboard refused; the text stays selectable
    }
  }
  return (
    <Card padding="md" className="flex flex-col gap-4">
      <div>
        <h2 className="text-caption text-fg-muted">{t('info.project')}</h2>
        {job.project_id ? (
          <Link to={`/jobs?project=${job.project_id}`} className="text-body text-primary-text hover:underline">
            {project?.name ?? job.project_id}
          </Link>
        ) : (
          <p className="text-body text-fg">{t('info.noProject')}</p>
        )}
      </div>
      {(text || shots.length > 0) && (
        <div>
          <div className="flex items-center justify-between">
            <h2 className="text-caption text-fg-muted">{shots.length ? t('info.shots') : t('info.prompt')}</h2>
            <IconButton size="sm" label={t('ui:action.copy')} icon={<Copy className="size-4" />} onClick={() => copy(shots.length ? shots.join('\n') : text)} />
          </div>
          {shots.length ? (
            <ol className="flex flex-col gap-1.5">
              {shots.map((s, i) => (
                <li key={i} className="text-body text-fg">
                  <span className="mr-1.5 text-caption text-fg-muted">{t('info.shotN', { n: i + 1 })}</span>
                  {s}
                </li>
              ))}
            </ol>
          ) : (
            <p className="text-body whitespace-pre-wrap text-fg">{text}</p>
          )}
        </div>
      )}
      <div>
        <h2 className="mb-1.5 text-caption text-fg-muted">{t('info.params')}</h2>
        <details className="group">
          <summary className="flex cursor-pointer list-none items-center justify-between gap-2 rounded-card border border-border px-3 py-2 text-body text-fg hover:bg-surface-2">
            <span className="truncate">{params.map(([, val]) => val).join(' · ')}</span>
            <ChevronDown aria-hidden className="size-4 shrink-0 text-fg-muted transition-transform group-open:rotate-180" />
          </summary>
          <dl className="mt-2 flex flex-col gap-1.5">
            {params.map(([k, val]) => (
              <Row key={k} label={k} value={val} />
            ))}
          </dl>
        </details>
      </div>
    </Card>
  )
}

/** Retrying a failed step starts a new task linked to this one. */
function useRetryStep(bizId: string) {
  const { t } = useTranslation('job')
  const navigate = useNavigate()
  const toast = useToast()
  return useMutation({
    mutationFn: (node: string) => jobsApi.retryNode(bizId, node),
    onSuccess: (res) => {
      toast(t('done.retried'))
      navigate(`/jobs/${res.biz_id}`)
    },
    onError: (err) => toast(errorText(t, err)),
  })
}

function ExecutionDetails({ job }: { job: JobDetail }) {
  const { t } = useTranslation('job')
  const retry = useRetryStep(job.biz_id)
  return (
    <details className="group rounded-card border border-border bg-surface">
      <summary className="flex h-12 cursor-pointer list-none items-center justify-between px-4 text-body font-semibold text-fg">
        {t('exec.title')}
        <ChevronDown aria-hidden className="size-4 text-fg-muted transition-transform group-open:rotate-180" />
      </summary>
      <div className="border-t border-border px-4 py-3">
        <p className="mb-2 text-caption text-fg-muted">{t('exec.note')}</p>
        <ul className="flex flex-col divide-y divide-border">
          {job.nodes.map((n) => (
            <li key={n.name} className="flex flex-wrap items-center gap-3 py-2">
              <span className="min-w-0 flex-1 text-body text-fg">{nodeLabel(t, n)}</span>
              {n.attempt > 1 && <span className="text-caption text-fg-muted">{t('exec.attempt', { n: n.attempt })}</span>}
              {n.started_at && <ElapsedTime start={n.started_at} end={n.finished_at ?? (ACTIVE.includes(n.status) ? null : n.started_at)} className="text-caption text-fg-muted" />}
              <StatusPill status={n.status === 'waiting' || n.status === 'ready' || n.status === 'pending' ? (n.status === 'pending' ? 'queued' : 'running') : n.status === 'suspended' ? 'awaiting_review' : n.status === 'skipped' ? 'cancelled' : n.status} />
              {n.status === 'failed' && RETRYABLE[job.workflow_name] === n.name && (
                <Button size="sm" loading={retry.isPending} onClick={() => retry.mutate(n.name)}>
                  {t('action.retryStep')}
                </Button>
              )}
              {n.status === 'failed' && n.error_code && <p className="w-full text-caption text-danger-fg">{failureText(t, n.error_code)}</p>}
            </li>
          ))}
        </ul>
      </div>
    </details>
  )
}

function ProgressPanel({ job }: { job: JobDetail }) {
  const { t, i18n } = useTranslation('job')
  const stream = useStream()
  const total = job.nodes.length
  const done = job.nodes.filter((n) => n.status === 'succeeded' || n.status === 'skipped').length
  const reason = job.nodes.find((n) => n.queue_reason)?.queue_reason
  return (
    <Card padding="lg" className="flex flex-col gap-3">
      <h2 className="text-section font-semibold text-fg">{job.status === 'cancelling' ? t('progress.cancelling') : job.status === 'queued' ? t('progress.queued') : t('progress.title')}</h2>
      <IndeterminateProgress label={t('progress.title')} />
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-caption text-fg-muted">
        <ElapsedTime start={job.started_at ?? job.created_at} />
        {total > 0 && <span>{t('progress.steps', { done, total })}</span>}
        {reason && ['capacity', 'user_limit', 'backoff'].includes(reason) && <span>{t(`progress.reason.${reason}`)}</span>}
        {stream.status !== 'offline' && <LastSync at={stream.lastSyncAt} />}
      </div>
      {stream.status === 'offline' && (
        <div role="status" className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-card bg-warning-soft px-3 py-2 text-caption text-warning-fg">
          <WifiOff aria-hidden className="size-4 shrink-0 text-warning" />
          <span className="flex-1 tabular-nums">
            {stream.lastSyncAt ? t('progress.offline', { time: formatClock(stream.lastSyncAt, i18n.language) }) : t('progress.offlineNoSync')}
          </span>
          <Button size="sm" onClick={stream.reconnect}>
            {t('progress.reconnect')}
          </Button>
        </div>
      )}
      <p className="text-caption text-fg-muted">{t('progress.note')}</p>
    </Card>
  )
}

export default function JobDetailPage() {
  const { t, i18n } = useTranslation('job')
  const { bizId = '' } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const toast = useToast()
  const stream = useStream()
  const [pending, setPending] = useState<null | 'cancel' | 'delete' | 'rename'>(null)
  const [title, setTitle] = useState('')
  const query = useQuery({
    queryKey: keys.jobs.detail(bizId),
    queryFn: () => jobsApi.get(bizId),
    // The stream refreshes this; poll only while it is down and the job is live.
    refetchInterval: (q) => (stream.status !== 'open' && q.state.data && ACTIVE.includes(q.state.data.status) ? 5000 : false),
  })
  const job = query.data
  const refresh = () => {
    qc.invalidateQueries({ queryKey: keys.jobs.detail(bizId) })
    qc.invalidateQueries({ queryKey: keys.jobs.all })
  }
  const retry = useRetryStep(bizId)
  const onError = (err: unknown) => toast(errorText(t, err))
  const cancel = useMutation({ mutationFn: () => jobsApi.cancel(bizId), onSuccess: () => (setPending(null), toast(t('done.cancelling')), refresh()), onError })
  const remove = useMutation({ mutationFn: () => jobsApi.remove(bizId), onSuccess: () => (toast(t('done.deleted')), refresh(), navigate('/jobs')), onError })
  const rename = useMutation({ mutationFn: () => jobsApi.rename(bizId, title.trim()), onSuccess: () => (setPending(null), refresh()), onError })

  if (query.isPending) {
    return (
      <div className="mx-auto flex max-w-[1280px] flex-col gap-4 px-4 py-6 lg:px-6" aria-busy="true">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="aspect-[3/2] w-full max-w-3xl" />
      </div>
    )
  }
  if (!job) {
    const notFound = query.error instanceof ApiError && query.error.status === 404
    return <ErrorState message={notFound ? t('notFound') : errorText(t, query.error)} onRetry={notFound ? undefined : () => query.refetch()} />
  }

  const label = job.title || t(`tasks:type.${job.workflow_name}`, { defaultValue: t('tasks:untitled') })
  const assets = resultAssets(job)
  const active = ACTIVE.includes(job.status)
  const createAgain = () => navigate('/', { state: { prefillJob: { workflowName: job.workflow_name, spec: job.spec } } })
  const suggestions: SuggestedAction[] =
    job.status === 'succeeded' ? suggestActions(job as unknown as JobResponse, resolveTab(job.workflow_name), assets, i18n.getFixedT(null, 'translation')) : []
  const applySuggestion = (s: SuggestedAction) => {
    if (s.kind === 'save-character' || s.kind === 'save-frame-character') navigate('/characters', { state: { prefillAssetId: s.sourceAssetId } })
    else if (s.kind === 'more-batch') createAgain()
    else navigate('/', { state: { prefillSuggestion: s } })
  }
  const retryable = job.nodes.find((n) => n.status === 'failed' && RETRYABLE[job.workflow_name] === n.name)
  const took = job.finished_at && job.started_at ? formatDuration(new Date(job.finished_at).getTime() - new Date(job.started_at).getTime(), t) : null

  return (
    <div className="mx-auto flex max-w-[1280px] flex-col gap-5 px-4 py-6 lg:px-6">
      <Link to="/jobs" className="inline-flex w-fit items-center gap-1.5 text-body text-fg-muted hover:text-fg">
        <ArrowLeft aria-hidden className="size-4" />
        {t('back')}
      </Link>

      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h1 className="truncate text-title font-semibold text-fg">{label}</h1>
            <IconButton size="sm" label={t('rename')} icon={<PencilLine className="size-4" />} onClick={() => (setTitle(job.title), setPending('rename'))} />
          </div>
          <p className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-fg-muted">
            <span>
              {t('id')} <span className="font-mono">{job.biz_id.slice(-8)}</span>
            </span>
            <span>{t('createdAt', { time: formatDateTime(job.created_at, i18n.language) })}</span>
            {job.retry_of_job_id && (
              <Link to={`/jobs/${job.retry_of_job_id}`} className="text-primary-text hover:underline">
                {t('tasks:retryOf')}
              </Link>
            )}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <StatusPill status={job.status} />
          {active ? (
            <ElapsedTime start={job.started_at ?? job.created_at} className="text-caption text-fg-muted" />
          ) : (
            took && <span className="text-caption text-fg-muted tabular-nums">{t('duration', { duration: took })}</span>
          )}
          <Button icon={<RotateCcw aria-hidden className="size-4" />} onClick={createAgain}>
            {t('action.createAgain')}
          </Button>
          {['queued', 'running', 'awaiting_review'].includes(job.status) && (
            <Button variant="danger-outline" onClick={() => setPending('cancel')}>
              {t('action.cancel')}
            </Button>
          )}
          {TERMINAL.includes(job.status) && (
            <Button variant="danger-outline" onClick={() => setPending('delete')}>
              {t('action.delete')}
            </Button>
          )}
        </div>
      </header>

      {job.status === 'awaiting_review' ? (
        <ReviewPanel job={job} />
      ) : (
        <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_400px]">
          <div className="flex min-w-0 flex-col gap-5">
            {active && <ProgressPanel job={job} />}
            {(job.status === 'failed' || job.status === 'partial') && (
              <Card padding="lg" className="flex flex-col gap-2 border-danger">
                <h2 className="text-section font-semibold text-danger-fg">{job.status === 'failed' ? t('failed.title') : t('failed.partial')}</h2>
                <p className="text-body text-fg">{failureText(t, job.error_code || job.nodes.find((n) => n.status === 'failed')?.error_code)}</p>
                <p className="text-caption text-fg-muted">{t('failed.hint')}</p>
                <div className="flex flex-wrap gap-2">
                  <Button variant="primary" onClick={createAgain}>
                    {t('action.editAndRetry')}
                  </Button>
                  {retryable && (
                    <Button loading={retry.isPending} onClick={() => retry.mutate(retryable.name)}>
                      {t('action.retryStep')}
                    </Button>
                  )}
                </div>
              </Card>
            )}
            {job.status === 'cancelled' && (
              <Card padding="lg">
                <h2 className="text-section font-semibold text-fg">{t('cancelled.title')}</h2>
                <p className="text-body text-fg-muted">{t('cancelled.hint')}</p>
              </Card>
            )}
            {assets.length > 0 && <ResultViewer assetIds={assets} />}
            {suggestions.length > 0 && (
              <section aria-labelledby="next-title" className="flex flex-wrap items-center gap-2">
                <h2 id="next-title" className="text-caption text-fg-muted">
                  {t('next.title')}
                </h2>
                {suggestions.map((s) => (
                  <button key={s.kind} type="button" onClick={() => applySuggestion(s)} className={buttonClasses('ghost', 'sm')}>
                    {s.label}
                  </button>
                ))}
              </section>
            )}
            <ExecutionDetails job={job} />
          </div>
          <div className="flex flex-col gap-5">
            <CreditsCard job={job} />
            <InfoCard job={job} />
          </div>
        </div>
      )}
      {job.status === 'awaiting_review' && <ExecutionDetails job={job} />}

      <ConfirmDialog
        open={pending === 'cancel'}
        onOpenChange={(o) => !o && setPending(null)}
        title={t('confirmCancel.title')}
        target={label}
        effects={[t('confirmCancel.effect1'), t('confirmCancel.effect2')]}
        confirmLabel={t('action.cancel')}
        danger
        busy={cancel.isPending}
        onConfirm={() => cancel.mutate()}
      />
      <ConfirmDialog
        open={pending === 'delete'}
        onOpenChange={(o) => !o && setPending(null)}
        title={t('confirmDelete.title')}
        target={label}
        effects={[t('confirmDelete.effect1'), t('confirmDelete.effect2')]}
        confirmLabel={t('action.delete')}
        danger
        busy={remove.isPending}
        onConfirm={() => remove.mutate()}
      />
      <Dialog
        open={pending === 'rename'}
        onOpenChange={(o) => !o && setPending(null)}
        title={t('rename')}
        locked={rename.isPending}
        footer={
          <>
            <Button onClick={() => setPending(null)}>{t('ui:action.cancel')}</Button>
            <Button variant="primary" loading={rename.isPending} disabled={!title.trim() || [...title.trim()].length > 128} onClick={() => rename.mutate()}>
              {t('ui:action.save')}
            </Button>
          </>
        }
      >
        <Field label={t('tasks:rename.label')}>
          <Input value={title} onChange={(e) => setTitle(e.target.value)} autoFocus />
        </Field>
      </Dialog>
    </div>
  )
}
