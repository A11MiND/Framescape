import type { Tab } from './jobResult'

// §19.4.1's "生成中預計 N 分鐘" gap. No historical per-job timing table
// exists yet (that would need aggregating real job started_at/finished_at
// deltas, a separate feature on its own), so these are rough, hand-picked
// upper-bound seconds per workflow — deliberately conservative (better to
// under-promise) rather than a false precision this codebase has no data to
// back up. Revisit once real timing data exists.
const BASE_SECONDS: Record<Tab, number> = {
  'image.single': 20,
  'image.batch': 35,
  'image.comic4': 60,
  'image.sequence': 50,
  'video.single': 150,
  'video.sequence': 150,
}

// video generation time scales with duration_seconds/resolution far more
// than image generation scales with n — this stays a rough multiplier, not
// a real regression fit.
export function estimateWaitSeconds(tab: Tab, opts: { n?: number; shots?: number; durationSeconds?: number; resolution?: string } = {}): number {
  const base = BASE_SECONDS[tab]
  if (tab === 'image.batch') return base + (Math.max(opts.n ?? 4, 1) - 1) * 8
  if (tab === 'image.sequence') return base * Math.max(opts.shots ?? 1, 1)
  if (tab === 'video.single') {
    const durationFactor = (opts.durationSeconds ?? 5) / 5
    const resFactor = opts.resolution === '2K' ? 1.6 : 1
    return Math.round(base * durationFactor * resFactor)
  }
  if (tab === 'video.sequence') {
    return Math.round(base * Math.max(opts.shots ?? 1, 1) * ((opts.durationSeconds ?? 5) / 5))
  }
  return base
}

export function formatWaitMinutes(seconds: number): string {
  const minutes = Math.max(1, Math.round(seconds / 60))
  return String(minutes)
}
