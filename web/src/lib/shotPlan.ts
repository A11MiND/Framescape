// Mirrors jobsvc.planShots' exact anchor/mode logic (video_sequence.go)
// so §19.4.4's shot cards can show each segment's real continuity
// strategy before submitting — "r2va·角色" / "i2va·尾帧" — instead of
// making the user infer it from a tooltip. idx is 1-based, matching the
// backend's own indexing; hasCharacterAnchor mirrors characterRefAsset's
// "only the first bound character (slot A) counts" rule.
export type ShotMode = 'r2va' | 't2va' | 'i2va'

export function shotMode(idx: number, recalibrateEvery: number, hasCharacterAnchor: boolean): ShotMode {
  const interval = recalibrateEvery > 0 ? recalibrateEvery : 3
  const isAnchor = idx === 1 || (idx - 1) % interval === 0
  if (isAnchor && hasCharacterAnchor) return 'r2va'
  if (isAnchor) return 't2va'
  return 'i2va'
}

export const SHOT_MODE_LABEL: Record<ShotMode, string> = {
  r2va: 'r2va · 角色锚定',
  t2va: 't2va · 纯文字',
  i2va: 'i2va · 尾帧续接',
}

export const SHOT_MODE_CLASS: Record<ShotMode, string> = {
  r2va: 'border-violet-600 bg-violet-500/10 text-violet-300',
  t2va: 'border-zinc-700 bg-zinc-900 text-zinc-400',
  i2va: 'border-sky-700 bg-sky-500/10 text-sky-300',
}
