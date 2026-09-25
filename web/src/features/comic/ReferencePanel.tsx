import { useTranslation } from 'react-i18next'
import { Field, Input } from '../../ui'
import type { ComicDocument } from '../../lib/comicDocument'
import { ReferencePicker } from '../create/ReferencePicker'
import type { ComicLimits } from './StoryPanel'

interface Props {
  doc: ComicDocument
  limits: ComicLimits
  formats?: string[]
  maxBytes?: number
  onChange: (doc: ComicDocument) => void
  disabled: boolean
}

/** Character and style references, each with what it is for. */
export function ReferencePanel({ doc, limits, formats, maxBytes, onChange, disabled }: Props) {
  const { t } = useTranslation('comic')
  const ids = doc.references.map((r) => r.asset_id)
  return (
    <section aria-labelledby="comic-refs" className="flex flex-col gap-3">
      <h2 id="comic-refs" className="text-label font-semibold text-fg">
        {t('inputs.references', { n: doc.references.length, max: limits.references })}
      </h2>
      <fieldset disabled={disabled} className="contents">
        <ReferencePicker
          label={t('inputs.referencesPick')}
          value={ids}
          limits={{ max: limits.references, formats, maxBytes }}
          onChange={(next) =>
            onChange({ ...doc, references: next.map((id) => doc.references.find((r) => r.asset_id === id) ?? { asset_id: id, label: '' }) })
          }
        />
      </fieldset>
      {doc.references.map((r, i) => (
        <Field key={r.asset_id} label={t('inputs.referenceUse', { n: i + 1 })}>
          <Input
            value={r.label}
            maxLength={limits.referenceLabel}
            disabled={disabled}
            placeholder={t('inputs.referenceUsePlaceholder')}
            onChange={(e) => onChange({ ...doc, references: doc.references.map((v, n) => (n === i ? { ...v, label: e.target.value } : v)) })}
          />
        </Field>
      ))}
      <p className="text-caption text-fg-muted">{doc.references.length ? t('inputs.referencesCost') : t('inputs.referencesHint')}</p>
    </section>
  )
}
