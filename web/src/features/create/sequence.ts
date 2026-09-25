// Shots of an image sequence. Each shot has a stable id so a reference keeps
// pointing at the same shot when the order changes; the spec sent to the
// server uses 1-based positions instead.

export type SequenceMode = 'quick' | 'continuity'

export interface SequenceShot {
  id: string
  text: string
  /** The id of an earlier shot whose image this one builds on. */
  ref: string | null
}

/** A reference that had to be cleared, in shot numbers after the change. */
export interface RefReset {
  shot: number
  ref: number
  reason: 'order' | 'removed'
}

export function newShot(text = ''): SequenceShot {
  return { id: crypto.randomUUID(), text, ref: null }
}

// A reference must point at a shot placed before this one.
function clearInvalidRefs(shots: SequenceShot[]): { shots: SequenceShot[]; resets: RefReset[] } {
  const pos = new Map(shots.map((s, i) => [s.id, i]))
  const resets: RefReset[] = []
  const next = shots.map((s, i) => {
    if (s.ref === null) return s
    const at = pos.get(s.ref)
    if (at !== undefined && at < i) return s
    if (at !== undefined) resets.push({ shot: i + 1, ref: at + 1, reason: 'order' })
    return { ...s, ref: null }
  })
  return { shots: next, resets }
}

/** Moves a shot; references that would now point forward are cleared and reported. */
export function moveShot(shots: SequenceShot[], from: number, to: number): { shots: SequenceShot[]; resets: RefReset[] } {
  if (from === to || from < 0 || to < 0 || from >= shots.length || to >= shots.length) return { shots, resets: [] }
  const next = [...shots]
  const [moved] = next.splice(from, 1)
  next.splice(to, 0, moved)
  return clearInvalidRefs(next)
}

/** Removes a shot; shots that referenced it lose that reference, reported with its old number. */
export function removeShot(shots: SequenceShot[], id: string): { shots: SequenceShot[]; resets: RefReset[] } {
  const removedAt = shots.findIndex((s) => s.id === id)
  if (removedAt < 0) return { shots, resets: [] }
  const resets: RefReset[] = []
  const next = shots
    .filter((s) => s.id !== id)
    .map((s, i) => {
      if (s.ref !== id) return s
      resets.push({ shot: i + 1, ref: removedAt + 1, reason: 'removed' })
      return { ...s, ref: null }
    })
  return { shots: next, resets }
}

/** Inserts a copy of a shot right after it; the copy keeps the same reference. */
export function duplicateShot(shots: SequenceShot[], id: string): SequenceShot[] {
  const at = shots.findIndex((s) => s.id === id)
  if (at < 0) return shots
  const next = [...shots]
  next.splice(at + 1, 0, { ...newShot(shots[at].text), ref: shots[at].ref })
  return next
}

export interface SequenceSpec {
  shots: string[]
  shot_source_refs?: number[]
}

/**
 * The spec fields for image.sequence. Empty shots are not sent, so positions
 * are renumbered; a reference to an empty shot is dropped.
 */
export function sequenceSpec(shots: SequenceShot[]): SequenceSpec {
  const kept = shots.filter((s) => s.text.trim())
  const pos = new Map(kept.map((s, i) => [s.id, i + 1]))
  const refs = kept.map((s) => (s.ref ? (pos.get(s.ref) ?? 0) : 0))
  return { shots: kept.map((s) => s.text), ...(refs.some((r) => r > 0) ? { shot_source_refs: refs } : {}) }
}

/** The shot a card builds on, as a 1-based number, or null. In continuity mode an unset reference means the previous shot. */
export function effectiveRef(shots: SequenceShot[], index: number, mode: SequenceMode): number | null {
  const ref = shots[index]?.ref
  if (ref) {
    const at = shots.findIndex((s) => s.id === ref)
    return at >= 0 && at < index ? at + 1 : null
  }
  return mode === 'continuity' && index > 0 ? index : null
}

/** Video sequences re-anchor every `every`-th shot (1-based), as the server plans them. */
export function isAnchorShot(index: number, every: number): boolean {
  const n = every > 0 ? every : 3
  return index === 0 || index % n === 0
}

/** The shot fields of a video sequence; references become manual reference picks. */
export function videoSequenceShots(shots: SequenceShot[]): { shots: string[]; shot_reference_overrides?: number[] } {
  const s = sequenceSpec(shots)
  return { shots: s.shots, ...(s.shot_source_refs ? { shot_reference_overrides: s.shot_source_refs } : {}) }
}
