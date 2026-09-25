import type { TFunction } from 'i18next'
import type { JobDetail, JobNode } from '../../../lib/api/jobs'
import type { JobResponse } from '../../../lib/api'
import { resolveTab, resultAssetIds as legacyResultAssetIds } from '../../../lib/jobResult'

export const ACTIVE = ['queued', 'running', 'awaiting_review', 'cancelling']
export const TERMINAL = ['succeeded', 'partial', 'failed', 'cancelled']

const num = (v: unknown) => (typeof v === 'number' ? v : Number.MAX_SAFE_INTEGER)

function assetsOf(n: JobNode): string[] {
  const out = n.outputs ?? {}
  const list = out['asset-ids']
  if (Array.isArray(list) && list.length) return list.filter((x): x is string => typeof x === 'string' && x !== '')
  const one = out['asset-id']
  return typeof one === 'string' && one ? [one] : []
}

/** The job's result assets, from the nodes its plan marks as results. */
export function resultAssets(job: JobDetail): string[] {
  const results = job.nodes
    .map((n, i) => ({ n, i }))
    .filter(({ n }) => n.display?.result === true && n.status === 'succeeded')
    .sort((a, b) => num(a.n.display?.shot) - num(b.n.display?.shot) || a.i - b.i)
  const ids = results.flatMap(({ n }) => assetsOf(n))
  if (ids.length || job.nodes.some((n) => n.display)) return [...new Set(ids)]
  // Jobs from the previous engine carry no display metadata.
  return legacyResultAssetIds(job as unknown as JobResponse, resolveTab(job.workflow_name))
}

export interface DraftShot {
  index: number
  assetId: string
  status: string
}

/** A preview-first video sequence's draft clips, in shot order. */
export function draftShots(job: JobDetail): DraftShot[] {
  return job.nodes
    .filter((n) => n.display?.group === 'draft' && typeof n.display?.shot === 'number')
    .map((n) => ({ index: n.display!.shot as number, assetId: assetsOf(n)[0] ?? '', status: n.status }))
    .sort((a, b) => a.index - b.index)
}

/** A readable name for an execution step. */
export function nodeLabel(t: TFunction, n: JobNode): string {
  const d = n.display ?? {}
  const shot = typeof d.shot === 'number' ? d.shot : undefined
  const panel = typeof d.panel === 'number' ? d.panel : undefined
  if (shot !== undefined) {
    if (n.name.endsWith('-extract')) return `${t('job:node.shot', { n: shot })} · ${t('job:node.extract')}`
    const g = d.group
    if (g === 'draft') return t('job:node.shotDraft', { n: shot })
    if (g === 'redo') return t('job:node.shotRedo', { n: shot })
    if (g === 'upgrade') return t('job:node.shotUpgrade', { n: shot })
    if (g === 'enhance') return t('job:node.shotEnhance', { n: shot })
    return t('job:node.shot', { n: shot })
  }
  if (panel !== undefined) return d.group === 'enhance' ? t('job:node.panelEnhance', { n: panel }) : t('job:node.panel', { n: panel })
  if (d.group === 'stylize') return t('job:node.stylize')
  const extract = n.name.match(/^shot-(\d+)-extract$/)
  if (extract) return `${t('job:node.shot', { n: Number(extract[1]) })} · ${t('job:node.extract')}`
  if (['gen', 'compose', 'concat', 'gate', 'enhance', 'split'].includes(n.name)) return t(`job:node.${n.name}`)
  return t('job:node.step')
}

/** The settings worth showing for a job, as label/value pairs. */
export function paramRows(t: TFunction, job: JobDetail): [string, string][] {
  const s = job.spec as Record<string, unknown>
  const rows: [string, string][] = [[t('job:info.type'), t(`tasks:type.${['image.single', 'image.sequence', 'image.comic4', 'video.single', 'video.sequence'].includes(job.workflow_name) ? job.workflow_name : 'unknown'}`)]]
  const str = (k: string) => (typeof s[k] === 'string' && s[k] ? (s[k] as string) : undefined)
  if (str('image_provider')) rows.push([t('job:info.provider'), (s.image_provider as string) === 'openai' ? 'OpenAI' : (s.image_provider as string) === 'gemini' ? 'Gemini' : 'MiniMax'])
  else if (job.workflow_name.startsWith('video')) rows.push([t('job:info.provider'), 'MiniMax'])
  if (typeof s.n === 'number' && s.n > 1) rows.push([t('job:info.count'), String(s.n)])
  if (str('aspect_ratio')) rows.push([t('job:info.ratio'), s.aspect_ratio as string])
  if (str('ratio')) rows.push([t('job:info.ratio'), s.ratio as string])
  if (str('image_size')) rows.push([t('job:info.size'), s.image_size as string])
  if (str('image_quality')) rows.push([t('job:info.quality'), s.image_quality as string])
  if (str('resolution')) rows.push([t('job:info.resolution'), s.resolution as string])
  if (typeof s.duration_seconds === 'number') rows.push([t('job:info.durationSec'), t('job:info.seconds', { n: s.duration_seconds })])
  return rows
}
