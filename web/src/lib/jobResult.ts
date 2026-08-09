import type { JobResponse } from './api'

export type Tab =
  | 'image.single'
  | 'image.batch'
  | 'image.comic4'
  | 'image.sequence'
  | 'video.single'
  | 'video.sequence'

// Which job_nodes node + output field carries the result asset id(s) for
// each form — see workflows/*.json (image-comic4's "compose" node is the
// final tiled grid; image-sequence's "shots" node is a Loop whose aggregate
// output is already an array; video-single's "gen" node's asset-id is the
// generated clip itself, not the extracted first/last frames; video.sequence's
// "concat" node only exists once the preview gate has been resumed and the
// merge finished — before that, this lookup harmlessly finds nothing).
// Shared between Studio (inline results) and JobDetail (/jobs/:bizId) so the
// two never drift on what "the result" means for a given workflow.
export const RESULT_FIELD: Record<Tab, { node: string; field: string }> = {
  'image.single': { node: 'gen', field: 'asset-ids' },
  'image.batch': { node: 'gen', field: 'asset-ids' },
  'image.comic4': { node: 'compose', field: 'asset-id' },
  'image.sequence': { node: 'shots', field: 'asset-id' },
  'video.single': { node: 'gen', field: 'asset-id' },
  'video.sequence': { node: 'concat', field: 'asset-id' },
}

export function resultAssetIds(job: JobResponse | undefined, tab: Tab): string[] {
  if (!job) return []
  const { node, field } = RESULT_FIELD[tab]
  const n = job.nodes.find((x) => x.name === node)
  const v = n?.outputs?.[field]
  if (Array.isArray(v)) return v as string[]
  if (typeof v === 'string') return [v]
  return []
}
