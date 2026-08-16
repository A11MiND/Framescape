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
// i18n key for each workflow's human-readable label, shared so Studio's tab
// nav and JobDetail's page heading (when a job has no title of its own,
// e.g. an F5.4 auto-split job predating the title-derivation fix) never
// show a raw workflow_name string like "image.comic4" to the user. Callers
// do `t(WORKFLOW_LABEL_KEY[tab])` — a plain key map rather than resolved
// strings, so this stays a dependency-free module even though the labels
// themselves are locale-dependent.
export const WORKFLOW_LABEL_KEY: Record<Tab, string> = {
  'image.single': 'workflow.imageSingle',
  'image.batch': 'workflow.imageBatch',
  'image.comic4': 'workflow.imageComic4',
  'image.sequence': 'workflow.imageSequence',
  'video.single': 'workflow.videoSingle',
  'video.sequence': 'workflow.videoSequence',
}

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

// jobs.status (GET /jobs, GET /jobs/{bizID}) is lowercase business-layer
// vocabulary ("running"/"succeeded"/"failed"/"cancelled" — jobsvc.go's
// normalizeStatus); PhaseBadge's table is keyed on Aether's own
// capitalized phase enum. This is the one mapping between the two, shared
// so JobDetail and the jobs list can't drift on what each status renders as.
export function statusToPhase(status: string): string {
  switch (status) {
    case 'succeeded':
      return 'Succeeded'
    case 'failed':
      return 'Failed'
    case 'cancelled':
      return 'Cancelled'
    default:
      return 'Running'
  }
}
