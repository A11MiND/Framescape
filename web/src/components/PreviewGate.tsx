import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
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
  const { t } = useTranslation()
  const shots = job.nodes
    .filter((n) => /^shot-\d+$/.test(n.name))
    .map((n) => ({
      index: Number(n.name.slice(5)),
      assetId: (n.outputs?.['asset-id'] as string | undefined) ?? '',
    }))
    .sort((a, b) => a.index - b.index)

  // job.spec.shots is 0-indexed; shot-N DAG nodes are 1-indexed (§5.4's
  // planShots: idx := i + 1, video_sequence.go) — this is the one place
  // that mapping matters client-side, to echo back what the user actually
  // wrote for each 768P draft they're now deciding on.
  const originalPrompts = job.spec.shots ?? []

  const [buckets, setBuckets] = useState<Record<number, Bucket>>({})
  const [overrides, setOverrides] = useState<Record<number, string>>({})

  const setBucket = (index: number, bucket: Bucket) =>
    setBuckets((cur) => ({ ...cur, [index]: bucket }))

  const upgradeCount = Object.values(buckets).filter((b) => b === 'upgrade').length
  const upgradeCost = estimateVideoCredits(duration, '2K') * upgradeCount
  // §19.4.5 P0: "必须同时显示「当前选择」和「全都升 2K」两个数字，让省钱这
  // 件事可感知" — the direct-2K comparison this whole preview-gate/768P
  // strategy exists to justify (§3.6).
  const allUpgradeCost = estimateVideoCredits(duration, '2K') * shots.length
  const savedPct = allUpgradeCost > 0 ? Math.round((1 - upgradeCost / allUpgradeCost) * 100) : 0

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
      <p className="text-sm text-zinc-400">{t('previewGate.intro', { count: shots.length })}</p>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        {shots.map((s) => (
          <div key={s.index} className="rounded-xl border border-zinc-800 bg-zinc-950 p-3">
            <p className="mb-2 text-xs text-zinc-500">{t('previewGate.segmentN', { n: s.index })}</p>
            <ShotPreview assetId={s.assetId} />
            {originalPrompts[s.index - 1] && (
              <p className="mt-1.5 line-clamp-2 text-xs text-zinc-500" title={originalPrompts[s.index - 1]}>
                {originalPrompts[s.index - 1]}
              </p>
            )}
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
                  {t(`previewGate.bucket.${b}`)}
                </button>
              ))}
            </div>
            {buckets[s.index] === 'redo' && (
              <input
                value={overrides[s.index] ?? ''}
                onChange={(e) => setOverrides((cur) => ({ ...cur, [s.index]: e.target.value }))}
                placeholder={t('workflowGraph.retryPromptPlaceholder')}
                className="mt-2 w-full rounded-md border border-zinc-800 bg-zinc-900 px-2 py-1 text-xs outline-none focus:border-violet-500"
              />
            )}
          </div>
        ))}
      </div>

      <div className="space-y-2 rounded-xl border border-zinc-800 bg-zinc-950 p-3">
        <p className="text-sm text-zinc-400">
          {t('previewGate.currentSelection', { count: upgradeCount })}
          {Object.values(buckets).filter((b) => b === 'redo').length > 0 &&
            ` · ${t('previewGate.redoCount', { count: Object.values(buckets).filter((b) => b === 'redo').length })}`}
        </p>
        <p className="text-sm text-zinc-300">
          {t('previewGate.willCost')} <span className="font-mono text-violet-300">✦ {upgradeCost}</span>
          <span className="text-zinc-600">
            {' '}
            {t('previewGate.compareOpen')}
            {t('previewGate.compareAllUpgrade')} <span className="font-mono">✦ {allUpgradeCost}</span>
            {upgradeCount > 0 && savedPct > 0 ? t('previewGate.savedPct', { pct: savedPct }) : ''}
            {t('previewGate.compareClose')}
          </span>
        </p>
        <div className="flex items-center gap-3 pt-1">
          <button
            onClick={() => resume.mutate()}
            disabled={resume.isPending}
            className="rounded-lg bg-violet-500 px-5 py-2.5 text-sm font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
          >
            {resume.isPending
              ? t('previewGate.submitting')
              : upgradeCount > 0
                ? t('previewGate.confirmWithUpgrade', { cost: upgradeCost })
                : t('previewGate.confirm')}
          </button>
          {resume.isError && <p className="text-sm text-red-400">{(resume.error as Error).message}</p>}
        </div>
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
