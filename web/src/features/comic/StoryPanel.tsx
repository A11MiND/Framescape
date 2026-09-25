import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { FileUp } from 'lucide-react'
import { Button, Field, Textarea } from '../../ui'
import { charCount, sourceExcerpts, type ComicDocument } from '../../lib/comicDocument'
import { importComicSource } from '../../lib/comicImport'

export interface ComicLimits {
  brief: number
  context: number
  background: number
  references: number
  referenceLabel: number
}

interface Props {
  doc: ComicDocument
  limits: ComicLimits
  checked: boolean
  onCheck: (checked: boolean) => void
  onChange: (doc: ComicDocument) => void
  run: (action: () => Promise<void>) => void
  onMessage: (text: string) => void
  onError: (text: string) => void
  disabled: boolean
}

/** Step 1: the story, and the source material whose reviewed excerpts are sent along. */
export function StoryPanel({ doc, limits, checked, onCheck, onChange, run, onMessage, onError, disabled }: Props) {
  const { t, i18n } = useTranslation('comic')
  const file = useRef<HTMLInputElement>(null)
  const n = (v: number) => v.toLocaleString(i18n.language)

  return (
    <div className="flex flex-col gap-4">
      <Field label={t('inputs.brief')} help={t('inputs.briefHint', { n: n(charCount(doc.brief)), max: n(limits.brief) })}>
        <Textarea value={doc.brief} disabled={disabled} maxChars={limits.brief} placeholder={t('inputs.briefPlaceholder')} onChange={(e) => onChange({ ...doc, brief: e.target.value })} className="min-h-48" />
      </Field>

      <details className="group rounded-card border border-border" open={Boolean(doc.background)}>
        <summary className="cursor-pointer list-none px-3 py-2.5 text-body font-semibold text-fg">{t('inputs.sources')}</summary>
        <div className="flex flex-col gap-3 border-t border-border p-3">
          <div>
            <Button size="sm" icon={<FileUp aria-hidden className="size-4" />} disabled={disabled} onClick={() => file.current?.click()}>
              {t('inputs.importSource')}
            </Button>
            <input
              ref={file}
              type="file"
              accept=".pdf,.txt,.md"
              aria-label={t('inputs.importSourceLabel')}
              className="sr-only"
              tabIndex={-1}
              onChange={(e) => {
                const f = e.target.files?.[0]
                e.target.value = ''
                if (f)
                  run(async () => {
                    const text = await importComicSource(f, (p) => t('content.page', { n: p }))
                    onChange({ ...doc, background: text, context: '' })
                    onMessage(t('done.sourceImported'))
                  })
              }}
            />
            <p className="mt-1 text-caption text-fg-muted">{t('inputs.sourceFormats')}</p>
          </div>
          <Field label={t('inputs.background')} help={t('inputs.backgroundHint', { n: n(charCount(doc.background)), max: n(limits.background) })}>
            <Textarea
              value={doc.background}
              disabled={disabled}
              onChange={(e) => {
                if (charCount(e.target.value) <= limits.background) onChange({ ...doc, background: e.target.value })
                else onError(t('error.backgroundTooLong', { max: n(limits.background) }))
              }}
              className="min-h-28"
            />
          </Field>
          <Button
            size="sm"
            disabled={disabled || !doc.background.trim()}
            onClick={() => onChange({ ...doc, context: sourceExcerpts(doc.background, doc.brief, (from, to) => t('content.range', { from, to }), Math.min(6500, limits.context)) })}
          >
            {t('inputs.extract')}
          </Button>
        </div>
      </details>

      <Field label={t('inputs.context')} help={t('inputs.contextHint', { n: n(charCount(doc.context)), max: n(limits.context) })}>
        <Textarea value={doc.context} disabled={disabled} maxChars={limits.context} onChange={(e) => onChange({ ...doc, context: e.target.value })} className="min-h-28" />
      </Field>
      {doc.context.trim() && (
        <label className="flex items-start gap-2 text-body text-fg">
          <input type="checkbox" className="mt-1 size-4 accent-primary" checked={checked} onChange={(e) => onCheck(e.target.checked)} />
          {t('inputs.checked')}
        </label>
      )}
    </div>
  )
}
