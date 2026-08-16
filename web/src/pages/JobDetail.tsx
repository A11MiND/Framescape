import { useEffect } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { resultAssetIds, statusToPhase, WORKFLOW_LABEL_KEY, type Tab } from '../lib/jobResult'
import { displayNodeError, firstSpecificError } from '../lib/errors'
import { useToast } from '../components/Toast'
import AppShell from '../components/AppShell'
import PhaseBadge from '../components/PhaseBadge'
import WorkflowGraph from '../components/WorkflowGraph'
import PreviewGate from '../components/PreviewGate'
import GenerationProgress from '../components/GenerationProgress'
import { useJobStream } from '../hooks/useJobStream'
import { api, ApiError } from '../lib/api'
import { useMutation, useQuery } from '@tanstack/react-query'

// PRD §19.4.6's job detail / DAG view — fully reconstructible from the URL
// alone (unlike Studio's inline results, which only exist for the job just
// submitted in that browser tab): fetches job + spec fresh from
// GET /jobs/{bizID} rather than relying on any client-side state.
export default function JobDetail() {
  const { t } = useTranslation()
  const { bizId } = useParams<{ bizId: string }>()
  const navigate = useNavigate()
  const pushToast = useToast()

  const jobQuery = useJobStream(bizId)

  useEffect(() => {
    if (jobQuery.isError) {
      pushToast(t('jobDetail.connectionUnstable'), () => jobQuery.refetch())
    }
  }, [jobQuery.isError, jobQuery.refetch, pushToast, t])

  const job = jobQuery.data
  const gateNode = job?.nodes.find((n) => n.name === 'gate')
  const gateSuspended = job?.workflow_name === 'video.sequence' && gateNode?.phase === 'Suspended'
  const assetIds = job ? resultAssetIds(job, job.workflow_name as Tab) : []
  const running = job && job.status !== 'succeeded' && job.status !== 'failed' && job.status !== 'cancelled'
  const label = job ? t(WORKFLOW_LABEL_KEY[job.workflow_name as Tab]) || job.workflow_name : ''

  // F7.4: stops the run and lets the existing terminal-phase machinery
  // (jobsvc.Service.Cancel's own doc) settle credits — this just fires the
  // request and refetches, no optimistic update, since the job's actual
  // status still only flips once the engine and projection layer catch up.
  const cancelJob = useMutation({
    mutationFn: () => api.cancelJob(bizId!),
    onSuccess: () => jobQuery.refetch(),
    onError: (err) => pushToast(err instanceof ApiError ? err.message : t('jobDetail.cancelFailed'), () => cancelJob.mutate()),
  })

  return (
    <AppShell>
      <div className="mx-auto max-w-5xl space-y-6 px-6 py-8">
        {!job && <p className="text-zinc-500">{t('common.loading')}</p>}

        {job && (
          <>
            <div className="flex items-center justify-between">
              <div>
                <h1 className="text-lg font-medium">
                  {!!job.retry_of_job_id && (
                    <Link
                      to={`/jobs/${job.retry_of_job_id}`}
                      title={t('jobs.retryIndicatorLinkTitle')}
                      className="mr-1 text-violet-400 hover:text-violet-300"
                      onClick={(e) => e.stopPropagation()}
                    >
                      ↻
                    </Link>
                  )}
                  {job.title || label}
                </h1>
                <p className="text-sm text-zinc-500">{label}</p>
              </div>
              <div className="flex items-center gap-3">
                <PhaseBadge phase={statusToPhase(job.status)} />
                {/* §07's "「以此再生成」在作業詳情頁本身缺獨立入口" gap —
                    AssetDetail already has this (regenerate()'s own doc
                    there); this is the same prefillJob round trip, just
                    triggered from the job itself rather than one of its
                    output assets, so it's available even for a job that
                    partially/fully failed and has no asset to click through. */}
                <button
                  onClick={() => navigate('/', { state: { prefillJob: { workflowName: job.workflow_name, spec: job.spec } } })}
                  className="rounded-lg border border-zinc-700 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-violet-500 hover:text-violet-300"
                >
                  ✏️ {t('assetDetail.regenerateFromThis')}
                </button>
                {running && (
                  <button
                    onClick={() => cancelJob.mutate()}
                    disabled={cancelJob.isPending}
                    className="rounded-lg border border-zinc-700 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-red-500 hover:text-red-400 disabled:opacity-50"
                  >
                    {cancelJob.isPending ? t('jobDetail.cancelling') : t('jobDetail.cancelJob')}
                  </button>
                )}
              </div>
            </div>

            {/* §07's "已消耗 / 預估 對比條" gap — job.credit_held is what's
                currently reserved, credit_settled is what's actually been
                spent so far (both already tracked since W7, just not
                echoed on this endpoint until now — handleGetJob's own
                doc). Only worth showing once something has actually been
                held; a job that hasn't started yet has nothing to compare. */}
            {job.credit_held > 0 && (
              <div className="flex items-center gap-4 rounded-xl border border-zinc-800 bg-zinc-900/60 px-4 py-2.5 font-mono text-xs text-zinc-400">
                <span>
                  {t('jobDetail.creditSettled')} <span className="text-zinc-200">✦{job.credit_settled}</span>
                </span>
                <span className="text-zinc-700">/</span>
                <span>
                  {t('jobDetail.creditHeld')} <span className="text-zinc-200">✦{job.credit_held}</span>
                </span>
                {job.credit_estimated > 0 && (
                  <>
                    <span className="text-zinc-700">/</span>
                    <span>
                      {t('jobDetail.creditEstimated')} <span className="text-zinc-200">✦{job.credit_estimated}</span>
                    </span>
                  </>
                )}
              </div>
            )}

            <WorkflowGraph job={job} />

            {job.status !== 'succeeded' && job.status !== 'failed' && !gateSuspended && (
              <div className="rounded-xl border border-zinc-800 bg-zinc-900 p-6">
                <GenerationProgress kind={job.workflow_name.startsWith('video') ? 'video' : 'image'} />
                {jobQuery.streamState === 'reconnecting' && (
                  <p className="mt-2 text-center text-xs text-amber-500">{t('jobDetail.reconnecting')}</p>
                )}
              </div>
            )}

            {gateSuspended && bizId && (
              <div className="rounded-xl border border-zinc-800 bg-zinc-900 p-6">
                <PreviewGate
                  bizId={bizId}
                  job={job}
                  duration={job.spec.duration_seconds ?? 5}
                  onResumed={() => jobQuery.refetch()}
                />
              </div>
            )}

            {job.status === 'failed' && (
              <p className="text-red-400">{displayNodeError(firstSpecificError(job.nodes), t)}</p>
            )}

            {job.status === 'succeeded' && assetIds.length > 0 && (
              <div className="flex flex-wrap gap-3 rounded-xl border border-zinc-800 bg-zinc-900 p-6">
                {assetIds.map((id) => (
                  <ResultAsset key={id} assetId={id} />
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </AppShell>
  )
}

// §07's "下載已完成功能仍缺" gap — AssetDetail already has this exact
// `<a download>` pattern for a single asset viewed on its own page; this is
// the same thing inline on the result grid so downloading doesn't require
// an extra click through to /assets/:id first.
function ResultAsset({ assetId }: { assetId: string }) {
  const { t } = useTranslation()
  const { data } = useQuery({ queryKey: ['asset', assetId], queryFn: () => api.getAsset(assetId) })
  if (!data) return <div className="h-48 w-48 animate-pulse rounded-lg bg-zinc-800" />
  return (
    <div className="group relative">
      {data.type === 'video' ? (
        <video src={data.public_url} controls className="h-48 w-48 rounded-lg bg-black object-contain" />
      ) : (
        <img src={data.public_url} alt="" className="h-48 w-48 rounded-lg object-cover" />
      )}
      <a
        href={data.public_url}
        download
        target="_blank"
        rel="noreferrer"
        title={t('assetDetail.download')}
        className="absolute right-1.5 top-1.5 rounded-lg bg-black/60 px-2 py-1 text-xs text-zinc-100 opacity-0 backdrop-blur transition group-hover:opacity-100"
      >
        ⬇
      </a>
    </div>
  )
}
