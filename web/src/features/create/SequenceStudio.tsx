import { useEffect, useRef, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { ArrowRight, Plus, TriangleAlert } from 'lucide-react'
import { Button, Card, ChoiceCards, Field, SegmentedControl, Select } from '../../ui'
import { createApi, type CreateRequest } from '../../lib/api/create'
import { projectsApi } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { hasOpenAIImage } from '../../lib/api/account'
import { formatNumber } from '../../lib/format'
import { useMe } from '../../app/useMe'
import { useCurrentProject } from '../../app/currentProject'
import { CreateNav } from './CreateNav'
import { ReferencePicker } from './ReferencePicker'
import { CharacterPicker } from './CharacterPicker'
import { PresetPicker } from './PresetPicker'
import { CostBar } from './CostBar'
import { ShotCard } from './ShotCard'
import { useShotResults } from './shotResults'
import { GptConfirm, GptSizeControl } from './gptOptions'
import { useGptChoices, useSizeLabel } from './gptSizes'
import { useDraft } from './drafts'
import { useSubmit } from './useSubmit'
import { characterSlots, readPrefill } from './prefill'
import { duplicateShot, moveShot, newShot, removeShot, sequenceSpec, type RefReset, type SequenceMode, type SequenceShot } from './sequence'
import { useCharacterPrefill } from './useCharacterPrefill'
import { usePresetPrefill } from './usePresetPrefill'

type Provider = 'minimax' | 'openai'

interface SequenceDraft {
  shots: SequenceShot[]
  mode: SequenceMode
  provider: Provider
  size: string
  quality: string
  reference: string[]
  characters: string[]
  presets: string[]
  project: string
  lastJob: string
  /** The ids of the shots sent with lastJob, in the order the server numbered them. */
  lastShotIds: string[]
}

const initialDraft = (): SequenceDraft => ({
  shots: [newShot(), newShot()],
  mode: 'continuity',
  provider: 'minimax',
  size: '',
  quality: '',
  reference: [],
  characters: [],
  presets: [],
  project: '',
  lastJob: '',
  lastShotIds: [],
})
const INITIAL = initialDraft()

/** Image sequence (spec P02): numbered shots that can build on earlier ones. */
export default function SequenceStudio() {
  const { t, i18n } = useTranslation('create')
  const location = useLocation()
  const navigate = useNavigate()
  const me = useMe()
  const [currentProject] = useCurrentProject(me.data?.biz_id)
  const [draft, update] = useDraft<SequenceDraft>(me.data?.biz_id, 'image-sequence', INITIAL)
  useCharacterPrefill(draft.characters, (characters) => update({ characters }))
  usePresetPrefill(draft.presets, (presets) => update({ presets }))
  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities, staleTime: 60_000 })
  const characters = useQuery({ queryKey: keys.characters, queryFn: createApi.characters })
  const presets = useQuery({ queryKey: keys.presets, queryFn: createApi.presets })
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list })
  const [resets, setResets] = useState<RefReset[]>([])
  const [moved, setMoved] = useState('')
  const [armed, setArmed] = useState<string | null>(null)
  const dragFrom = useRef<number | null>(null)
  const [confirming, setConfirming] = useState(false)
  const sizeLabel = useSizeLabel()

  const openai = caps.data?.providers?.openai
  const gptReason = !openai?.enabled ? t('nav.badge.unavailable') : me.data && !hasOpenAIImage(me.data) ? t('nav.badge.beta') : null
  const gpt = draft.provider === 'openai'
  const { size, quality, gone } = useGptChoices(gpt ? openai : undefined, draft, update)

  // A prefill (create again, or the GPT page's sequence output) seeds the draft once.
  useEffect(() => {
    const state = location.state as { sequenceProvider?: Provider } | null
    const p = readPrefill(location.state, 'image.sequence')
    if (!p && !state?.sequenceProvider) return
    const patch: Partial<SequenceDraft> = {}
    if (state?.sequenceProvider) patch.provider = state.sequenceProvider
    if (p) {
      const shots = (p.shots?.length ? p.shots : ['']).map((text) => newShot(text))
      ;(p.shotRefs ?? []).forEach((r, i) => {
        if (shots[i] && r > 0 && r <= i) shots[i].ref = shots[r - 1].id
      })
      Object.assign(patch, {
        shots,
        mode: p.sequenceMode ?? 'quick',
        provider: p.provider ?? 'minimax',
        reference: p.reference ?? [],
        characters: p.characters ?? [],
        presets: p.presets ?? [],
        ...(p.size ? { size: p.size } : {}),
        ...(p.quality ? { quality: p.quality } : {}),
      })
    }
    update(patch)
    setResets([])
    navigate('.', { replace: true, state: {} })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state])

  const maxShots = caps.data?.image.sequence_max_shots ?? 12
  const maxChars = caps.data?.image.max_prompt_chars ?? 1500
  const shots = draft.shots.length ? draft.shots : INITIAL.shots
  const spec = sequenceSpec(shots)
  const empty = shots.length - spec.shots.length
  const tooLong = shots.some((s) => [...s.text].length > maxChars)
  const project = draft.project || currentProject || ''
  const { results } = useShotResults(draft.lastJob, draft.lastShotIds)

  const setShots = (next: SequenceShot[], changed: RefReset[] = []) => {
    update({ shots: next })
    setResets(changed)
  }
  const move = (from: number, to: number) => {
    const r = moveShot(shots, from, to)
    setShots(r.shots, r.resets)
    setMoved(t('sequence.move.moved', { n: to + 1 }))
  }

  const request: CreateRequest | null =
    spec.shots.length > 0 && spec.shots.length <= maxShots && !tooLong && (!gpt || !gptReason)
      ? {
          workflow_name: 'image.sequence',
          spec: {
            ...spec,
            ...(draft.mode === 'continuity' ? { image_sequence_mode: 'continuity' } : {}),
            ...(draft.reference[0] ? { source_image_asset_id: draft.reference[0] } : {}),
            ...(draft.characters.length ? { characters: characterSlots(draft.characters) } : {}),
            ...(draft.presets.length ? { preset_ids: draft.presets } : {}),
            ...(gpt ? { image_provider: 'openai', image_size: size.value, image_quality: quality.value } : {}),
          },
          ...(project ? { project_id: project } : {}),
        }
      : null
  const sentIds = shots.filter((s) => s.text.trim()).map((s) => s.id)
  const job = useSubmit(request, (bizId) => {
    setConfirming(false)
    update({ lastJob: bizId, lastShotIds: sentIds })
  })
  const credits = job.estimate?.data?.credits_total
  const count = empty > 0 ? t('sequence.countSkipped', { n: spec.shots.length, empty }) : t('sequence.count', { n: spec.shots.length })

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6">
      <CreateNav mode="image-sequence" />
      <div className="grid items-start gap-6 xl:grid-cols-[400px_minmax(0,1fr)]">
        <Card padding="none" className="order-2 flex flex-col gap-5 p-4 lg:p-5 xl:order-1">
          <div className="flex flex-col gap-1.5">
            <span className="text-label font-medium text-fg">{t('sequence.provider.label')}</span>
            <SegmentedControl<Provider>
              fullWidth
              label={t('sequence.provider.label')}
              value={draft.provider}
              onChange={(v) => update({ provider: v })}
              options={[
                { value: 'minimax', label: t('sequence.provider.minimax') },
                { value: 'openai', label: t('sequence.provider.openai'), disabled: Boolean(gptReason) && !gpt },
              ]}
            />
            {gptReason ? (
              <p className={gpt ? 'text-caption text-warning-fg' : 'text-caption text-fg-muted'}>{t('sequence.gptLocked', { reason: gptReason })}</p>
            ) : (
              gpt && <p className="text-caption text-fg-muted">{t('sequence.provider.openaiHint')}</p>
            )}
          </div>
          {gpt && openai && !gptReason && (
            <>
              {gone && (
                <p role="status" className="rounded-card bg-warning-soft p-2.5 text-caption text-warning-fg">
                  {gone}
                </p>
              )}
              <GptSizeControl sizes={openai.sizes} value={size.value} onChange={(v) => update({ size: v })} />
              <Field label={t('gpt.quality')}>
                <Select value={quality.value} onChange={(e) => update({ quality: e.target.value })}>
                  {openai.qualities.map((q) => (
                    <option key={q} value={q}>
                      {t(`gpt.qualityOpt.${q}`, { defaultValue: q })}
                    </option>
                  ))}
                </Select>
              </Field>
            </>
          )}
          <ReferencePicker
            label={t('sequence.initialRef.label')}
            value={draft.reference}
            onChange={(ids) => update({ reference: ids.slice(-1) })}
            limits={{ max: 1, ...(gpt && openai ? { formats: openai.reference_formats, maxBytes: openai.max_reference_bytes } : {}) }}
          />
          <p className="-mt-3 text-caption text-fg-muted">{t('sequence.initialRef.help')}</p>
          <CharacterPicker characters={characters.data?.characters ?? []} value={draft.characters} onChange={(ids) => update({ characters: ids })} />
          <PresetPicker presets={presets.data?.presets ?? []} value={draft.presets} onChange={(ids) => update({ presets: ids })} />
          <Field label={t('project.label')}>
            <Select value={project} onChange={(e) => update({ project: e.target.value })}>
              <option value="">{t('project.none')}</option>
              {(projects.data?.projects ?? []).map((p) => (
                <option key={p.biz_id} value={p.biz_id}>
                  {p.name}
                </option>
              ))}
            </Select>
          </Field>
        </Card>

        <div className="order-1 flex min-w-0 flex-col gap-5 xl:order-2">
          <div className="flex flex-col gap-4 2xl:flex-row 2xl:items-center 2xl:justify-between">
            <header className="flex min-w-0 flex-col gap-1">
              <h1 className="text-title font-semibold text-fg">{t('sequence.title')}</h1>
              <p className="text-body text-fg-muted">{t('sequence.description')}</p>
            </header>
            <ChoiceCards<SequenceMode>
              className="2xl:w-[560px] 2xl:shrink-0"
              label={t('sequence.mode.label')}
              value={draft.mode}
              onChange={(m) => update({ mode: m })}
              choices={[
                { value: 'quick', title: t('sequence.mode.quick.title'), description: t('sequence.mode.quick.body') },
                { value: 'continuity', title: t('sequence.mode.continuity.title'), description: t('sequence.mode.continuity.body') },
              ]}
            />
          </div>

          {resets.length > 0 && (
            <div role="status" className="flex gap-3 rounded-card border border-warning bg-warning-soft p-3 text-body text-warning-fg">
              <TriangleAlert aria-hidden className="mt-0.5 size-5 shrink-0 text-warning" />
              <ul className="flex-1">
                {resets.map((r) => (
                  <li key={`${r.shot}-${r.ref}`}>{t(`sequence.reset.${r.reason}`, { shot: r.shot, ref: r.ref })}</li>
                ))}
              </ul>
              <Button size="sm" variant="ghost" onClick={() => setResets([])}>
                {t('sequence.reset.dismiss')}
              </Button>
            </div>
          )}
          <p aria-live="polite" className="sr-only">
            {moved}
          </p>

          <section aria-labelledby="shots-title" className="flex flex-col gap-3">
            <div className="flex items-center justify-between gap-3">
              <h2 id="shots-title" className="text-section font-semibold text-fg">
                {t('sequence.shots')}
              </h2>
              {draft.lastJob && (
                <Link to={`/jobs/${draft.lastJob}`} className="inline-flex items-center gap-1 text-caption font-medium text-primary-text hover:underline">
                  {t('result.viewTask')}
                  <ArrowRight aria-hidden className="size-3.5" />
                </Link>
              )}
            </div>
            <ol className="grid gap-3 sm:grid-cols-[repeat(auto-fill,minmax(260px,1fr))]">
              {shots.map((s, i) => (
                <ShotCard
                  key={s.id}
                  shots={shots}
                  index={i}
                  mode={draft.mode}
                  maxChars={maxChars}
                  result={results[s.id]}
                  disabled={job.submitting}
                  canAdd={shots.length < maxShots}
                  dragging={armed === s.id}
                  onText={(text) => setShots(shots.map((x) => (x.id === s.id ? { ...x, text } : x)))}
                  onRef={(ref) => setShots(shots.map((x) => (x.id === s.id ? { ...x, ref } : x)))}
                  onMove={(to) => move(i, to)}
                  onDuplicate={() => setShots(duplicateShot(shots, s.id))}
                  onRemove={() => {
                    const r = removeShot(shots, s.id)
                    setShots(r.shots, r.resets)
                  }}
                  onDragArm={() => {
                    setArmed(s.id)
                    dragFrom.current = i
                  }}
                  onDragOver={(e) => dragFrom.current !== null && e.preventDefault()}
                  onDrop={() => {
                    if (dragFrom.current !== null) move(dragFrom.current, i)
                    dragFrom.current = null
                    setArmed(null)
                  }}
                  onDragEnd={() => {
                    dragFrom.current = null
                    setArmed(null)
                  }}
                />
              ))}
              <li className="flex min-h-40">
                <button
                  type="button"
                  disabled={shots.length >= maxShots || job.submitting}
                  onClick={() => setShots([...shots, newShot()])}
                  className="flex min-h-24 w-full flex-col items-center justify-center gap-1 rounded-card border border-dashed border-border-control text-body font-medium text-primary-text hover:bg-primary-soft disabled:cursor-not-allowed disabled:text-fg-muted disabled:hover:bg-transparent"
                >
                  <Plus aria-hidden className="size-5" />
                  {shots.length >= maxShots ? t('sequence.max', { max: maxShots }) : t('sequence.add')}
                </button>
              </li>
            </ol>
          </section>

          <Card padding="none" className="sticky bottom-0 z-10 p-4 shadow-overlay lg:p-5">
            <CostBar
              ready={Boolean(request)}
              credits={credits}
              calculating={Boolean(request) && job.stale}
              failed={Boolean(job.estimate?.isError)}
              reason={job.estimateError}
              usageBased={gpt}
              detail={count}
              notice={job.notice}
              actionLabel={gpt ? t('action.reviewCost') : t('sequence.generate')}
              onAction={gpt ? () => setConfirming(true) : job.submit}
              loading={!gpt && job.submitting}
              disabled={!job.canSubmit}
            />
          </Card>
        </div>
      </div>
      {gpt && openai && (
        <GptConfirm
          open={confirming}
          onOpenChange={setConfirming}
          job={job}
          rows={[
            [t('confirm.model'), openai.image_model],
            [t('confirm.size'), sizeLabel(size.value)],
            [t('confirm.quality'), t(`gpt.qualityOpt.${quality.value}`, { defaultValue: quality.value })],
            [t('confirm.count'), String(spec.shots.length)],
            [t('confirm.reservation'), credits === undefined ? t('ui:unknownValue') : t('cost.value', { n: formatNumber(credits, i18n.language) })],
          ]}
        />
      )}
    </div>
  )
}
