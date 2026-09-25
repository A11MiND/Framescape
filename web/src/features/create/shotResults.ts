import { useQuery } from '@tanstack/react-query'
import { jobsApi, type JobDetail } from '../../lib/api/jobs'
import { keys } from '../../lib/api/keys'
import { useStream } from '../../lib/stream/context'
import { ACTIVE } from '../tasks/detail/model'

export interface ShotResult {
  status: string
  assetId?: string
  errorCode?: string
}

/** The last job's result per shot id: node shot-N is the Nth shot that was sent. */
export function useShotResults(jobId: string, shotIds: string[]): { results: Record<string, ShotResult>; job: JobDetail | undefined } {
  const stream = useStream()
  const job = useQuery({
    queryKey: keys.jobs.detail(jobId),
    queryFn: () => jobsApi.get(jobId),
    enabled: Boolean(jobId),
    refetchInterval: (q) => (stream.status !== 'open' && q.state.data && ACTIVE.includes(q.state.data.status) ? 5000 : false),
  })
  const out: Record<string, ShotResult> = {}
  for (const n of job.data?.nodes ?? []) {
    const m = n.name.match(/^shot-(\d+)$/)
    const id = m ? shotIds[Number(m[1]) - 1] : undefined
    if (!id) continue
    const outputs = (n.outputs ?? {}) as { 'asset-id'?: string }
    out[id] = { status: n.status, assetId: n.status === 'succeeded' ? outputs['asset-id'] : undefined, errorCode: n.error_code }
  }
  return { results: out, job: job.data }
}

