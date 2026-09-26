import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown } from 'lucide-react'
import { Button, Drawer, Field, Input, Select, Textarea } from '../../ui'
import { charactersApi, type Character } from '../../lib/api/characters'
import { projectsApi } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { useToast } from '../../components/Toast'
import { ReferencePicker } from '../create/ReferencePicker'
import { useCharacterLimits } from './limits'

const SEED_MAX = 2 ** 31 - 1
const IMAGE_FORMATS = ['image/png', 'image/jpeg', 'image/webp']

/** Creates a character, or edits one when `character` is given; `initialRefs` seeds a new one. */
export function CharacterDrawer({
  open,
  onOpenChange,
  character,
  initialRefs = [],
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  character?: Character
  initialRefs?: string[]
}) {
  const { t } = useTranslation('characters')
  const qc = useQueryClient()
  const toast = useToast()
  const { maxRefs, nameMax, descriptionMax } = useCharacterLimits()
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list, enabled: open })
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [refs, setRefs] = useState<string[]>([])
  const [project, setProject] = useState('')
  const [seed, setSeed] = useState('')
  const [advanced, setAdvanced] = useState(false)
  const [tried, setTried] = useState(false)

  useEffect(() => {
    if (!open) return
    setName(character?.name ?? '')
    setDescription(character?.description ?? '')
    setRefs(character?.ref_asset_ids ?? initialRefs.slice(0, maxRefs))
    setProject(character?.project_id ?? '')
    setSeed(character ? String(character.seed) : '')
    setAdvanced(false)
    setTried(false)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, character])

  const seedValue = seed.trim() === '' ? undefined : Number(seed)
  const seedBad = seedValue !== undefined && (!Number.isInteger(seedValue) || seedValue < 0 || seedValue > SEED_MAX)
  const nameBad = !name.trim()
  const refsBad = refs.length === 0
  const invalid = nameBad || refsBad || seedBad

  const save = useMutation({
    mutationFn: () => {
      const body = { name: name.trim(), description, ref_asset_ids: refs, ...(seedValue !== undefined ? { seed: seedValue } : {}) }
      if (!character) return charactersApi.create({ ...body, ...(project ? { project_id: project } : {}) })
      return charactersApi.update(character.biz_id, { ...body, ...(project ? { project_id: project } : character.project_id ? { clear_project: true } : {}) })
    },
    onSuccess: () => {
      toast(character ? t('form.saved') : t('form.created'))
      qc.invalidateQueries({ queryKey: keys.characters })
      qc.invalidateQueries({ queryKey: keys.projects.all })
      onOpenChange(false)
    },
    onError: (err) => toast(errorText(t, err)),
  })

  const submit = () => {
    setTried(true)
    if (seedBad) setAdvanced(true)
    if (!invalid) save.mutate()
  }

  return (
    <Drawer
      open={open}
      onOpenChange={(o) => !save.isPending && onOpenChange(o)}
      title={character ? t('form.editTitle') : t('form.createTitle')}
      footer={
        <Button variant="primary" className="w-full" loading={save.isPending} onClick={submit}>
          {t('form.save')}
        </Button>
      }
    >
      <form
        className="flex flex-col gap-5"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        <Field label={t('form.name')} required error={tried && nameBad ? t('form.nameRequired') : undefined}>
          <Input value={name} maxLength={nameMax} placeholder={t('form.namePlaceholder')} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label={t('form.description')} optional help={t('form.descriptionHelp')}>
          <Textarea value={description} maxChars={descriptionMax} rows={4} placeholder={t('form.descriptionPlaceholder')} onChange={(e) => setDescription(e.target.value)} />
        </Field>
        <div className="flex flex-col gap-1.5">
          <ReferencePicker value={refs} onChange={setRefs} limits={{ max: maxRefs, formats: IMAGE_FORMATS }} label={t('form.refs')} required />
          <p className="text-caption text-fg-muted">{t('form.refsHelp', { max: maxRefs })}</p>
          {tried && refsBad && (
            <p role="alert" className="text-caption text-danger-fg">
              {t('form.refsRequired')}
            </p>
          )}
        </div>
        <Field label={t('form.project')} optional>
          <Select value={project} onChange={(e) => setProject(e.target.value)}>
            <option value="">{t('noProject')}</option>
            {(projects.data?.projects ?? []).map((p) => (
              <option key={p.biz_id} value={p.biz_id}>
                {p.name}
              </option>
            ))}
          </Select>
        </Field>
        <section className="rounded-card border border-border">
          <button type="button" aria-expanded={advanced} onClick={() => setAdvanced(!advanced)} className="flex w-full items-center justify-between px-3 py-2.5 text-label font-medium text-fg">
            {t('form.advanced')}
            <ChevronDown aria-hidden className={`size-4 text-fg-muted transition-transform ${advanced ? 'rotate-180' : ''}`} />
          </button>
          {advanced && (
            <div className="border-t border-border p-3">
              <Field label={t('form.seed')} optional help={t('form.seedHelp')} error={seedBad ? t('form.seedInvalid') : undefined}>
                <Input value={seed} inputMode="numeric" placeholder={t('form.seedPlaceholder')} onChange={(e) => setSeed(e.target.value.replace(/[^\d]/g, ''))} />
              </Field>
            </div>
          )}
        </section>
      </form>
    </Drawer>
  )
}
