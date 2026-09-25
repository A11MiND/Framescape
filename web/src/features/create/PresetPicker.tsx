import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, Palette } from 'lucide-react'
import { Button, Drawer, cn } from '../../ui'
import { presetDisplayName, type Preset } from '../../lib/api'

const CATEGORIES = ['style', 'pose', 'composition', 'lighting', 'camera'] as const

/** Selected presets summarized in the composer; chosen in a drawer by category. */
export function PresetPicker({ presets, value, onChange }: { presets: Preset[]; value: string[]; onChange: (ids: string[]) => void }) {
  const { t, i18n } = useTranslation('create')
  const [open, setOpen] = useState(false)
  const chosen = value.map((id) => presets.find((p) => p.biz_id === id)).filter((p): p is Preset => Boolean(p))
  const toggle = (id: string) => onChange(value.includes(id) ? value.filter((x) => x !== id) : [...value, id])
  return (
    <fieldset className="flex flex-col gap-1.5">
      <legend className="mb-1.5 text-label font-medium text-fg">
        {t('presets.label')}
        <span className="ml-1 font-normal text-fg-muted">{t('ui:field.optional')}</span>
      </legend>
      <Button className="justify-start" icon={<Palette aria-hidden className="size-4" />} onClick={() => setOpen(true)} aria-haspopup="dialog">
        <span className="truncate">{chosen.length ? chosen.map((p) => presetDisplayName(p, i18n.language)).join(t('ui:listSeparator')) : t('presets.choose')}</span>
      </Button>
      <Drawer
        open={open}
        onOpenChange={setOpen}
        title={t('presets.title')}
        description={chosen.length ? t('presets.selected', { n: chosen.length }) : undefined}
        footer={
          <>
            <Button onClick={() => onChange([])} disabled={!value.length}>
              {t('presets.clear')}
            </Button>
            <Button variant="primary" onClick={() => setOpen(false)}>
              {t('presets.done')}
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-5">
          {CATEGORIES.map((cat) => {
            const list = presets.filter((p) => p.category === cat)
            if (!list.length) return null
            return (
              <section key={cat} aria-labelledby={`preset-${cat}`}>
                <h3 id={`preset-${cat}`} className="mb-2 text-caption font-medium text-fg-muted">
                  {t(`presets.category.${cat}`)}
                </h3>
                <ul className="grid grid-cols-2 gap-2">
                  {list.map((p) => {
                    const on = value.includes(p.biz_id)
                    return (
                      <li key={p.biz_id}>
                        <button
                          type="button"
                          aria-pressed={on}
                          onClick={() => toggle(p.biz_id)}
                          className={cn('relative block w-full overflow-hidden rounded-thumb border-2 text-left', on ? 'border-primary' : 'border-transparent')}
                        >
                          {p.cover_url ? (
                            <img src={p.cover_url} alt="" className="aspect-[4/3] w-full object-cover" />
                          ) : (
                            <span aria-hidden className="block aspect-[4/3] w-full bg-surface-2" />
                          )}
                          <span className="block truncate px-2 py-1.5 text-caption text-fg">{presetDisplayName(p, i18n.language)}</span>
                          {on && (
                            <span className="absolute top-1.5 right-1.5 inline-flex size-6 items-center justify-center rounded-full bg-primary text-white">
                              <Check aria-hidden className="size-4" />
                            </span>
                          )}
                        </button>
                      </li>
                    )
                  })}
                </ul>
              </section>
            )
          })}
        </div>
      </Drawer>
    </fieldset>
  )
}
