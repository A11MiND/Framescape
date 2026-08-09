import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { api, type JobResponse } from '../lib/api'
import { estimateVideoCredits } from '../lib/pricing'

type Bucket = 'keep' | 'redo' | 'upgrade'

// F6.8/§5.5's preview gate: once the N-shot 768P draft chain finishes, the
// workflow suspends at its `gate` task (job.nodes[].phase === "Suspended";
// job.status itself stays "running" — only terminal phases update it, see
// jobsvc.Service.Get). The user reviews every shot and buckets each into
// keep / redo (regenerate at 768P) / upgrade (resubmit at full 2K price),
// then POST /jobs/{bizID}/resume carries that decision back to the engine.
// A shot can only be in one bucket — the backend's own bucketing switch
// (video_sequence.go's Resume) checks redo before upgrade if both were ever
// set for the same index, so enforcing one bucket per shot here isn't just
// UI tidiness, it's what keeps the two decision surfaces from disagreeing.
export default function PreviewGate({
  bizId,
  job,
  duration,
  onResumed,
}: {
  bizId: string
  job: JobResponse
  duration: number
  onResumed: () => void
}) {
  const shots = job.nodes
    .filter((n) => /^shot-\d+$/.test(n.name))
    .map((n) => ({
      index: Number(n.name.slice(5)),
      assetId: (n.outputs?.['asset-id'] as string | undefined) ?? '',
    }))
    .sort((a, b) => a.index - b.index)

  const [buckets, setBuckets] = useState<Record<number, Bucket>>({})
  const [overrides, setOverrides] = useState<Record<number, string>>({})

  const setBucket = (index: number, bucket: Bucket) =>
    setBuckets((cur) => ({ ...cur, [index]: bucket }))

  const upgradeCount = Object.values(buckets).filter((b) => b === 'upgrade').length
  const upgradeCost = estimateVideoCredits(duration, '2K') * upgradeCount

  const resume = useMutation({
    mutationFn: () => {
      const selected_shots = shots.filter((s) => buckets[s.index] === 'upgrade').map((s) => s.index)
      const redo_shots = shots.filter((s) => buckets[s.index] === 'redo').map((s) => s.index)
      const redo_prompt_overrides: Record<number, string> = {}
      for (const idx of redo_shots) {
        if (overrides[idx]?.trim()) redo_prompt_overrides[idx] = overrides[idx].trim()
      }
      return api.resumeJob(bizId, { selected_shots, redo_shots, redo_prompt_overrides })
    },
    onSuccess: onResumed,
  })

  return (
    <div className="w-full space-y-4">
      <p className="text-sm text-zinc-400">
        {shots.length} 段已生成 768P 草稿，逐段选择保留 / 重做 / 升级 2K
      </p>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        {shots.map((s) => (
          <div key={s.index} className="rounded-xl border border-zinc-800 bg-zinc-950 p-3">
            <p className="mb-2 text-xs text-zinc-500">第 {s.index} 段</p>
            <ShotPreview assetId={s.assetId} />
            <div className="mt-2 flex gap-1 text-xs">
              {(['keep', 'redo', 'upgrade'] as Bucket[]).map((b) => (
                <button
                  key={b}
                  onClick={() => setBucket(s.index, b)}
                  className={`flex-1 rounded-md border px-1.5 py-1 transition ${
                    (buckets[s.index] ?? 'keep') === b
                      ? 'border-violet-500 bg-violet-500/20 text-violet-300'
                      : 'border-zinc-800 text-zinc-500 hover:border-zinc-700'
                  }`}
                >
                  {b === 'keep' ? '保留' : b === 'redo' ? '重做' : '升 2K'}
                </button>
              ))}
            </div>
            {buckets[s.index] === 'redo' && (
              <input
                value={overrides[s.index] ?? ''}
                onChange={(e) => setOverrides((cur) => ({ ...cur, [s.index]: e.target.value }))}
                placeholder="重做提示词（留空则用原提示词）"
                className="mt-2 w-full rounded-md border border-zinc-800 bg-zinc-900 px-2 py-1 text-xs outline-none focus:border-violet-500"
              />
            )}
          </div>
        ))}
      </div>

      <div className="flex items-center gap-3">
        <button
          onClick={() => resume.mutate()}
          disabled={resume.isPending}
          className="rounded-lg bg-violet-500 px-5 py-2.5 text-sm font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
        >
          {resume.isPending ? '提交中…' : `确认并合成${upgradeCount > 0 ? ` (✦ +${upgradeCost} 升级)` : ''}`}
        </button>
        {resume.isError && <p className="text-sm text-red-400">{(resume.error as Error).message}</p>}
      </div>
    </div>
  )
}

function ShotPreview({ assetId }: { assetId: string }) {
  const { data } = useQuery({
    queryKey: ['asset', assetId],
    queryFn: () => api.getAsset(assetId),
    enabled: !!assetId,
  })
  if (!data) return <div className="h-32 w-full animate-pulse rounded-lg bg-zinc-800" />
  return <video src={data.public_url} controls className="h-32 w-full rounded-lg bg-black object-contain" />
}
