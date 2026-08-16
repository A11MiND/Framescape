import type { TFunction } from 'i18next'
import type { JobResponse } from './api'
import type { Tab } from './jobResult'

// PRD §19.4.2's "预测式下一步推荐" — a P0 differentiator the PRD itself
// singles out ("这一条不是锦上添花"), rule-driven off product type per the
// §19.4.2 table, not a fixed menu. Every action below only fires when the
// data it needs is actually present in the job response — no fabricated
// affordances for data the backend doesn't return yet (e.g. image.comic4's
// auto-split mode doesn't expose its four generated panel texts anywhere
// in GET /jobs, so "转成分镜" only offers itself for hand-written panels).
export type SuggestedAction =
  | { kind: 'to-video'; label: string; icon: string; sourceAssetId: string }
  | { kind: 'more-batch'; label: string; icon: string }
  | { kind: 'save-character'; label: string; icon: string; sourceAssetId: string }
  | { kind: 'to-sequence'; label: string; icon: string; shots: string[] }
  | { kind: 'upgrade-2k'; label: string; icon: string }
  | { kind: 'save-frame-character'; label: string; icon: string; sourceAssetId: string }

export function suggestActions(job: JobResponse, tab: Tab, resultAssetIds: string[], t: TFunction): SuggestedAction[] {
  const out: SuggestedAction[] = []
  const firstAsset = resultAssetIds[0]

  if ((tab === 'image.single' || tab === 'image.batch') && firstAsset) {
    out.push({ kind: 'to-video', label: t('suggestions.toVideo'), icon: '🎬', sourceAssetId: firstAsset })
    out.push({ kind: 'more-batch', label: t('suggestions.moreBatch'), icon: '🔁' })
    if (!job.spec.characters?.length) {
      out.push({ kind: 'save-character', label: t('suggestions.saveCharacter'), icon: '☺', sourceAssetId: firstAsset })
    }
  }

  if (tab === 'image.comic4' && job.spec.panels?.length) {
    out.push({ kind: 'to-sequence', label: t('suggestions.toSequence'), icon: '✨', shots: job.spec.panels })
  }

  if (tab === 'video.single') {
    if (job.spec.resolution === '768P') {
      out.push({ kind: 'upgrade-2k', label: t('suggestions.upgrade2k'), icon: '⬆' })
    }
    const extractNode = job.nodes.find((n) => n.name === 'extract')
    const frameAssetId = extractNode?.outputs?.['first-frame-asset-id'] as string | undefined
    if (frameAssetId) {
      out.push({ kind: 'save-frame-character', label: t('suggestions.saveFrameCharacter'), icon: '☺', sourceAssetId: frameAssetId })
    }
  }

  return out
}
