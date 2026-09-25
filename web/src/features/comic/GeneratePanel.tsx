import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Sparkles } from 'lucide-react'
import { Button, ChoiceCards, Field, IndeterminateProgress, Select, buttonClasses } from '../../ui'
import type { ComicDocument } from '../../lib/comicDocument'

export type AiState = 'ready' | 'beta' | 'unconfigured' | 'loading'

interface Props {
  doc: ComicDocument
  scope: number
  onScope: (panel: number) => void
  onMode: (mode: ComicDocument['mode']) => void
  model: string
  ai: AiState
  busy: boolean
  onReview: () => void
}

/** Step 2: how the page is drawn, what is redrawn, the model, and checking the cost. */
export function GeneratePanel({ doc, scope, onScope, onMode, model, ai, busy, onReview }: Props) {
  const { t } = useTranslation('comic')
  const pending = doc.pending
  return (
    <div className="flex flex-col gap-4">
      <ChoiceCards<ComicDocument['mode']>
        columns={1}
        label={t('inputs.modeLegend')}
        value={doc.mode}
        onChange={onMode}
        choices={[
          { value: 'editable', title: t('inputs.modeEditable'), description: t('generate.editableHint'), disabled: Boolean(pending) },
          { value: 'direct', title: t('inputs.modeDirect'), description: t('generate.directHint'), disabled: Boolean(pending) },
        ]}
      />
      {doc.mode === 'editable' && (
        <Field label={t('inputs.target')} help={scope > 0 ? t('generate.panelHint') : undefined}>
          <Select value={scope} disabled={Boolean(pending)} onChange={(e) => onScope(Number(e.target.value))}>
            <option value={0}>{t('inputs.targetPage')}</option>
            {[1, 2, 3, 4].map((i) => (
              <option key={i} value={i} disabled={!doc.page_asset_id}>
                {t('inputs.targetPanel', { n: i })}
              </option>
            ))}
          </Select>
        </Field>
      )}
      <p className="rounded-card bg-surface-2 px-3 py-2 text-caption text-fg">{t('generate.model', { model: model || '--' })}</p>
      {ai === 'beta' && <p className="rounded-card bg-warning-soft p-3 text-caption text-warning-fg">{t('inputs.betaRequired')}</p>}
      {ai === 'unconfigured' && <p className="text-caption text-fg-muted">{t('inputs.notConfigured')}</p>}
      {pending ? (
        <div role="status" className="flex flex-col gap-2 rounded-card border border-border p-3 text-body text-fg">
          {t('inputs.generating', { target: pending.panel ? t('inputs.targetPanelN', { n: pending.panel }) : t('inputs.targetWholePage') })}
          <IndeterminateProgress label={t('inputs.targetWholePage')} />
          <Link to={`/jobs/${pending.job_id}`} className={buttonClasses('ghost', 'sm')}>
            {t('inputs.viewTask')}
          </Link>
        </div>
      ) : (
        <Button variant="primary" size="lg" icon={<Sparkles aria-hidden className="size-5" />} loading={busy} disabled={ai !== 'ready'} onClick={onReview}>
          {t('inputs.reviewCost')}
        </Button>
      )}
    </div>
  )
}
