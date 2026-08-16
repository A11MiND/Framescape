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
// final tiled grid; video-single's "gen" node's asset-id is the generated
// clip itself, not the extracted first/last frames; video.sequence's
// "concat" node only exists once the preview gate has been resumed and the
// merge finished — before that, this lookup harmlessly finds nothing).
// image.sequence isn't listed here — see resultAssetIds' own special case.
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

export const RESULT_FIELD: Partial<Record<Tab, { node: string; field: string }>> = {
  'image.single': { node: 'gen', field: 'asset-ids' },
  'image.batch': { node: 'gen', field: 'asset-ids' },
  'image.comic4': { node: 'compose', field: 'asset-id' },
  'video.single': { node: 'gen', field: 'asset-id' },
  'video.sequence': { node: 'concat', field: 'asset-id' },
}

export function resultAssetIds(job: JobResponse | undefined, tab: Tab): string[] {
  if (!job) return []
  if (tab === 'image.sequence') {
    // image_sequence.go's own doc: each shot is its own "shot-N" DAG task
    // (video_sequence.go's "shot-%d" naming, reused for consistency) rather
    // than one Loop's aggregate output — cross-shot references mean shots
    // can depend on each other, which Aether's Loop can't express, so
    // there's no single node to read an array from anymore. Collects each
    // shot's own asset-id in shot order instead.
    const ids: string[] = []
    for (let i = 1; ; i++) {
      const n = job.nodes.find((x) => x.name === `shot-${i}`)
      if (!n) break
      const v = n.outputs?.['asset-id']
      if (typeof v === 'string' && v) ids.push(v)
    }
    return ids
  }
  const field = RESULT_FIELD[tab]
  if (!field) return []
  const n = job.nodes.find((x) => x.name === field.node)
  const v = n?.outputs?.[field.field]
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
