import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Plus, X } from 'lucide-react'
import { Menu, buttonClasses } from '../../ui'
import type { Character } from '../../lib/api'

export const MAX_CHARACTERS = 6

/** Bound characters as removable chips, with a menu to add more. */
export function CharacterPicker({ characters, value, onChange }: { characters: Character[]; value: string[]; onChange: (ids: string[]) => void }) {
  const { t } = useTranslation('create')
  const bound = value.map((id) => characters.find((c) => c.biz_id === id)).filter((c): c is Character => Boolean(c))
  const available = characters.filter((c) => !value.includes(c.biz_id))
  return (
    <fieldset className="flex flex-col gap-1.5">
      <legend className="mb-1.5 text-label font-medium text-fg">
        {t('character.label')}
        <span className="ml-1 font-normal text-fg-muted">{t('ui:field.optional')}</span>
      </legend>
      <div className="flex flex-wrap items-center gap-2">
        {bound.map((c) => (
          <span key={c.biz_id} className="inline-flex h-9 items-center gap-1.5 rounded-full border border-border-control bg-surface pr-1 pl-3 text-body text-fg">
            @{c.name}
            <span className="text-caption text-fg-muted">{t('character.refs', { n: c.ref_asset_ids.length })}</span>
            <button
              type="button"
              aria-label={t('character.remove', { name: c.name })}
              onClick={() => onChange(value.filter((id) => id !== c.biz_id))}
              className="inline-flex size-7 items-center justify-center rounded-full text-fg-muted hover:bg-surface-2 hover:text-fg"
            >
              <X aria-hidden className="size-3.5" />
            </button>
          </span>
        ))}
        {characters.length === 0 ? (
          <Link to="/characters" className="text-caption text-primary-text hover:underline">
            {t('character.empty')}
          </Link>
        ) : (
          value.length < MAX_CHARACTERS &&
          available.length > 0 && (
            <Menu
              align="start"
              trigger={
                <button type="button" className={buttonClasses('secondary', 'sm')}>
                  <Plus aria-hidden className="size-4" />
                  {t('character.add')}
                </button>
              }
              items={available.map((c) => ({ key: c.biz_id, label: `${c.name} · ${t('character.refs', { n: c.ref_asset_ids.length })}`, onSelect: () => onChange([...value, c.biz_id]) }))}
            />
          )
        )}
      </div>
    </fieldset>
  )
}
