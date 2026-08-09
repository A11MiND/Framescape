import { useEffect } from 'react'
import { useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api, type JobResponse } from '../lib/api'
import { resultAssetIds, type Tab } from '../lib/jobResult'
import { displayNodeError, firstSpecificError } from '../lib/errors'
import { useToast } from '../components/Toast'
import Nav from '../components/Nav'
import PhaseBadge from '../components/PhaseBadge'
import WorkflowGraph from '../components/WorkflowGraph'
import PreviewGate from '../components/PreviewGate'
import GenerationProgress from '../components/GenerationProgress'

// PRD §19.4.6's job detail / DAG view — fully reconstructible from the URL
// alone (unlike Studio's inline results, which only exist for the job just
// submitted in that browser tab): fetches job + spec fresh from
// GET /jobs/{bizID} rather than relying on any client-side state.
export default function JobDetail() {
  const { bizId } = useParams<{ bizId: string }>()
  const pushToast = useToast()

  const jobQuery = useQuery<JobResponse>({
    queryKey: ['job', bizId],
    queryFn: () => api.getJob(bizId!),
    enabled: !!bizId,
    refetchInterval: (query) => {
      const status = query.state.data?.status
      return status === 'succeeded' || status === 'failed' ? false : 1500
    },
  })

  useEffect(() => {
    if (jobQuery.isError) {
      pushToast('网络连接不稳定，无法获取作业状态', () => jobQuery.refetch())
    }
  }, [jobQuery.isError, jobQuery.refetch, pushToast])

  const job = jobQuery.data
  const gateNode = job?.nodes.find((n) => n.name === 'gate')
  const gateSuspended = job?.workflow_name === 'video.sequence' && gateNode?.phase === 'Suspended'
  const assetIds = job ? resultAssetIds(job, job.workflow_name as Tab) : []

  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-50">
      <Nav />
      <div className="mx-auto max-w-5xl space-y-6 px-6 py-8">
        {!job && <p className="text-zinc-500">加载中…</p>}

        {job && (
          <>
            <div className="flex items-center justify-between">
              <div>
                <h1 className="text-lg font-medium">{job.title}</h1>
                <p className="text-sm text-zinc-500">{job.workflow_name}</p>
              </div>
              <PhaseBadge phase={job.status === 'succeeded' ? 'Succeeded' : job.status === 'failed' ? 'Failed' : 'Running'} />
            </div>

            <WorkflowGraph job={job} />

            {job.status !== 'succeeded' && job.status !== 'failed' && !gateSuspended && (
              <div className="rounded-xl border border-zinc-800 bg-zinc-900 p-6">
                <GenerationProgress kind={job.workflow_name.startsWith('video') ? 'video' : 'image'} />
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
    </div>
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
