import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Copy, Lock, Sparkles, Trash2 } from 'lucide-react'
import { Button, Field, Input, Select, Textarea } from '../../ui'
import { presetDisplayName } from '../../lib/api'
import { PRESET_CATEGORIES, presetsApi, type Preset } from '../../lib/api/presets'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { useToast } from '../../components/Toast'
import { PresetCover } from './PresetCover'

const NAME_MAX = 64
const FRAGMENT_MAX = 512
const STYLE_TAG_MAX = 16

/**
 * One preset: a system preset is read-only with "save as my preset"; the
 * caller's own is edited in place; with no preset it creates a new one.
 */
export function PresetDetail({
  preset,
  defaultCategory,
  onApply,
  onSaved,
  onDelete,
}: {
  preset: Preset | null
  defaultCategory: string
  onApply: (p: Preset) => void
  onSaved: (p: Preset) => void
  onDelete: (p: Preset) => void
}) {
  const { t, i18n } = useTranslation('presets')
  const qc = useQueryClient()
  const toast = useToast()
  const system = preset !== null && !preset.mine
  const [name, setName] = useState('')
  const [fragment, setFragment] = useState('')
  const [category, setCategory] = useState(defaultCategory)
  const [styleTag, setStyleTag] = useState('')
  const [tried, setTried] = useState(false)

  useEffect(() => {
    setName(preset ? presetDisplayName(preset, i18n.language) : '')
    setFragment(preset?.prompt_fragment ?? '')
    setCategory(preset?.category ?? defaultCategory)
    setStyleTag(preset?.style_type ?? '')
    setTried(false)
  }, [preset, defaultCategory, i18n.language])

  const refresh = () => qc.invalidateQueries({ queryKey: keys.presets })
  const save = useMutation({
    mutationFn: () => {
      const body = { name: name.trim(), prompt_fragment: fragment.trim(), category, style_type: styleTag.trim() }
      return preset ? presetsApi.update(preset.biz_id, body) : presetsApi.create(body)
    },
    onSuccess: (p) => {
      toast(preset ? t('detail.saved') : t('detail.created'))
      refresh()
      onSaved(p)
    },
    onError: (err) => toast(errorText(t, err)),
  })
  const copy = useMutation({
    mutationFn: (p: Preset) =>
      presetsApi.create({
        name: (presetDisplayName(p, i18n.language) + t('detail.copySuffix')).slice(0, NAME_MAX),
        prompt_fragment: p.prompt_fragment,
        category: p.category,
        style_type: p.style_type,
      }),
    onSuccess: (p) => {
      toast(t('detail.copied'))
      refresh()
      onSaved(p)
    },
    onError: (err) => toast(errorText(t, err)),
  })

  const nameBad = !name.trim()
  const fragmentBad = !fragment.trim()
  const submit = () => {
    setTried(true)
    if (!nameBad && !fragmentBad) save.mutate()
  }

  return (
    <div className="flex flex-col gap-4">
      {preset && (
        <div className="flex gap-3">
          <PresetCover preset={preset} className="size-24 shrink-0 rounded-thumb" />
          <div className="flex min-w-0 flex-col gap-1.5">
            <p className="truncate text-body font-semibold text-fg">{presetDisplayName(preset, i18n.language)}</p>
            <div className="flex flex-wrap gap-1.5">
              <span className="rounded-badge bg-primary-soft px-2 py-0.5 text-badge font-medium text-primary-text">{system ? t('detail.systemTag') : t('detail.mineTag')}</span>
              <span className="rounded-badge bg-surface-2 px-2 py-0.5 text-badge text-fg">{t(`create:presets.category.${preset.category}`)}</span>
              {preset.style_type && <span className="rounded-badge bg-surface-2 px-2 py-0.5 text-badge text-fg-muted">{preset.style_type}</span>}
            </div>
            {preset.cover_url && <p className="text-caption text-fg-muted">{t('detail.coverNote')}</p>}
          </div>
        </div>
      )}
      <Field label={t('detail.fragment')} required={!system} help={t('detail.fragmentHelp')} error={tried && fragmentBad ? t('detail.fragmentRequired') : undefined}>
        <Textarea value={fragment} maxChars={FRAGMENT_MAX} rows={4} readOnly={system} onChange={(e) => setFragment(e.target.value)} />
      </Field>
      <Field label={t('detail.name')} required={!system} error={tried && nameBad ? t('detail.nameRequired') : undefined}>
        <Input value={name} maxLength={NAME_MAX} readOnly={system} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label={t('detail.category')}>
        <Select value={category} disabled={system} onChange={(e) => setCategory(e.target.value)}>
          {PRESET_CATEGORIES.map((c) => (
            <option key={c} value={c}>
              {t(`create:presets.category.${c}`)}
            </option>
          ))}
        </Select>
      </Field>
      <Field label={t('detail.styleTag')} optional={!system} help={t('detail.styleTagHelp')}>
        <Input value={styleTag} maxLength={STYLE_TAG_MAX} readOnly={system} onChange={(e) => setStyleTag(e.target.value)} />
      </Field>
      {system && (
        <p className="flex items-start gap-1.5 text-caption text-fg-muted">
          <Lock aria-hidden className="mt-0.5 size-3.5 shrink-0" />
          {t('detail.readOnly')}
        </p>
      )}
      <div className="grid gap-2 sm:grid-cols-2">
        {preset && (
          <Button variant="primary" icon={<Sparkles aria-hidden className="size-4" />} onClick={() => onApply(preset)}>
            {t('detail.apply')}
          </Button>
        )}
        {system ? (
          <Button icon={<Copy aria-hidden className="size-4" />} loading={copy.isPending} onClick={() => copy.mutate(preset)}>
            {t('detail.saveCopy')}
          </Button>
        ) : (
          <Button variant={preset ? 'secondary' : 'primary'} loading={save.isPending} onClick={submit}>
            {preset ? t('detail.save') : t('detail.createConfirm')}
          </Button>
        )}
        {preset?.mine && (
          <Button variant="danger-outline" className="sm:col-span-2" icon={<Trash2 aria-hidden className="size-4" />} onClick={() => onDelete(preset)}>
            {t('detail.delete')}
          </Button>
        )}
      </div>
    </div>
  )
}
