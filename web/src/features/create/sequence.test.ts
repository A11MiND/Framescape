import { describe, expect, it } from 'vitest'
import { duplicateShot, effectiveRef, isAnchorShot, moveShot, removeShot, sequenceSpec, videoSequenceShots, type SequenceShot } from './sequence'

const shot = (id: string, text: string, ref: string | null = null): SequenceShot => ({ id, text, ref })

describe('sequence shots', () => {
  it('keeps a reference on the same shot when the order changes', () => {
    const shots = [shot('a', 'harbour'), shot('b', 'tram'), shot('c', 'sunset', 'a')]
    const { shots: next, resets } = moveShot(shots, 2, 1)
    expect(next.map((s) => s.id)).toEqual(['a', 'c', 'b'])
    expect(next[1].ref).toBe('a')
    expect(resets).toEqual([])
    expect(sequenceSpec(next).shot_source_refs).toEqual([0, 1, 0])
  })

  it('clears a reference that would point at a later shot and names both shots', () => {
    const shots = [shot('a', 'harbour'), shot('b', 'tram', 'a'), shot('c', 'sunset')]
    const { shots: next, resets } = moveShot(shots, 1, 0)
    expect(next.map((s) => s.id)).toEqual(['b', 'a', 'c'])
    expect(next[0].ref).toBeNull()
    expect(resets).toEqual([{ shot: 1, ref: 2, reason: 'order' }])
  })

  it('clears references to a removed shot and reports its old number', () => {
    const shots = [shot('a', 'harbour'), shot('b', 'tram'), shot('c', 'sunset', 'b'), shot('d', 'night', 'a')]
    const { shots: next, resets } = removeShot(shots, 'b')
    expect(next.map((s) => [s.id, s.ref])).toEqual([['a', null], ['c', null], ['d', 'a']])
    expect(resets).toEqual([{ shot: 2, ref: 2, reason: 'removed' }])
  })

  it('does not send empty shots and renumbers references', () => {
    const shots = [shot('a', 'harbour'), shot('b', '  '), shot('c', 'sunset', 'a'), shot('d', 'night', 'b')]
    expect(sequenceSpec(shots)).toEqual({ shots: ['harbour', 'sunset', 'night'], shot_source_refs: [0, 1, 0] })
  })

  it('omits shot_source_refs when nothing is referenced', () => {
    expect(sequenceSpec([shot('a', 'x'), shot('b', 'y')])).toEqual({ shots: ['x', 'y'] })
  })

  it('duplicates a shot after itself with the same reference', () => {
    const next = duplicateShot([shot('a', 'x'), shot('b', 'y', 'a')], 'b')
    expect(next).toHaveLength(3)
    expect(next[2]).toMatchObject({ text: 'y', ref: 'a' })
    expect(next[2].id).not.toBe('b')
  })

  it('shows the previous shot as the reference in continuity mode', () => {
    const shots = [shot('a', 'x'), shot('b', 'y'), shot('c', 'z', 'a')]
    expect(effectiveRef(shots, 0, 'continuity')).toBeNull()
    expect(effectiveRef(shots, 1, 'continuity')).toBe(1)
    expect(effectiveRef(shots, 1, 'quick')).toBeNull()
    expect(effectiveRef(shots, 2, 'quick')).toBe(1)
  })
})

describe('video sequence shots', () => {
  it('anchors the first shot and every n-th after it, like the server', () => {
    expect([0, 1, 2, 3, 4, 5, 6].map((i) => isAnchorShot(i, 3))).toEqual([true, false, false, true, false, false, true])
    expect([0, 1, 2].map((i) => isAnchorShot(i, 0))).toEqual([true, false, false])
  })

  it('sends manual picks as reference overrides, renumbered past empty shots', () => {
    const shots = [shot('a', 'x'), shot('b', ''), shot('c', 'y'), shot('d', 'z', 'a')]
    expect(videoSequenceShots(shots)).toEqual({ shots: ['x', 'y', 'z'], shot_reference_overrides: [0, 0, 1] })
    expect(videoSequenceShots([shot('a', 'x')])).toEqual({ shots: ['x'] })
  })
})
