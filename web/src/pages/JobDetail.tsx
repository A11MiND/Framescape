import { useEffect } from 'react'
import { useParams } from 'react-router-dom'
import { resultAssetIds, statusToPhase, WORKFLOW_LABEL, type Tab } from '../lib/jobResult'
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
  const { bizId } = useParams<{ bizId: string }>()
  const pushToast = useToast()

  const jobQuery = useJobStream(bizId)

  useEffect(() => {
    if (jobQuery.isError) {
      pushToast('网络连接不稳定，无法获取作业状态', () => jobQuery.refetch())
    }
  }, [jobQuery.isError, jobQuery.refetch, pushToast])

  const job = jobQuery.data
  const gateNode = job?.nodes.find((n) => n.name === 'gate')
  const gateSuspended = job?.workflow_name === 'video.sequence' && gateNode?.phase === 'Suspended'
  const assetIds = job ? resultAssetIds(job, job.workflow_name as Tab) : []
  const running = job && job.status !== 'succeeded' && job.status !== 'failed' && job.status !== 'cancelled'

  // F7.4: stops the run and lets the existing terminal-phase machinery
  // (jobsvc.Service.Cancel's own doc) settle credits — this just fires the
  // request and refetches, no optimistic update, since the job's actual
  // status still only flips once the engine and projection layer catch up.
  const cancelJob = useMutation({
    mutationFn: () => api.cancelJob(bizId!),
    onSuccess: () => jobQuery.refetch(),
    onError: (err) => pushToast(err instanceof ApiError ? err.message : '取消失败，请重试', () => cancelJob.mutate()),
  })

  return (
    <AppShell>
      <div className="mx-auto max-w-5xl space-y-6 px-6 py-8">
        {!job && <p className="text-zinc-500">加载中…</p>}

        {job && (
          <>
            <div className="flex items-center justify-between">
              <div>
                <h1 className="text-lg font-medium">
                  {job.title || WORKFLOW_LABEL[job.workflow_name as Tab] || job.workflow_name}
                </h1>
                <p className="text-sm text-zinc-500">
                  {WORKFLOW_LABEL[job.workflow_name as Tab] ?? job.workflow_name}
                </p>
              </div>
              <div className="flex items-center gap-3">
                <PhaseBadge phase={statusToPhase(job.status)} />
                {running && (
                  <button
                    onClick={() => cancelJob.mutate()}
                    disabled={cancelJob.isPending}
                    className="rounded-lg border border-zinc-700 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-red-500 hover:text-red-400 disabled:opacity-50"
                  >
                    {cancelJob.isPending ? '取消中…' : '取消作业'}
                  </button>
                )}
              </div>
            </div>

            <WorkflowGraph job={job} />

            {job.status !== 'succeeded' && job.status !== 'failed' && !gateSuspended && (
              <div className="rounded-xl border border-zinc-800 bg-zinc-900 p-6">
                <GenerationProgress kind={job.workflow_name.startsWith('video') ? 'video' : 'image'} />
                {jobQuery.streamState === 'reconnecting' && (
                  <p className="mt-2 text-center text-xs text-amber-500">实时连接不稳定，重新连接中…</p>
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
              <p className="text-red-400">{displayNodeError(firstSpecificError(job.nodes))}</p>
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

function ResultAsset({ assetId }: { assetId: string }) {
  const { data } = useQuery({ queryKey: ['asset', assetId], queryFn: () => api.getAsset(assetId) })
  if (!data) return <div className="h-48 w-48 animate-pulse rounded-lg bg-zinc-800" />
  if (data.type === 'video') {
    return <video src={data.public_url} controls className="h-48 w-48 rounded-lg bg-black object-contain" />
  }
  return <img src={data.public_url} alt="" className="h-48 w-48 rounded-lg object-cover" />
}
