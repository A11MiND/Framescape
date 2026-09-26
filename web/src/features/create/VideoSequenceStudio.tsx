import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, ChevronDown, Copy, Film, GripVertical, Plus, Trash2, TriangleAlert } from 'lucide-react'
import { Button, Card, ChoiceCards, Field, IconButton, SegmentedControl, Select, Skeleton, StatusPill, Switch, Textarea, buttonClasses, cn } from '../../ui'
import { createApi, type CreateRequest } from '../../lib/api/create'
import { assetsApi } from '../../lib/api/assets'
import { projectsApi } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { formatNumber } from '../../lib/format'
import { useDebouncedValue } from '../../hooks/useDebouncedValue'
import { useMe } from '../../app/useMe'
import { useCurrentProject } from '../../app/currentProject'
import { stepPillStatus } from '../tasks/detail/model'
import { CreateNav } from './CreateNav'
import { ReferencePicker } from './ReferencePicker'
import { CharacterPicker } from './CharacterPicker'
import { CostBar } from './CostBar'
import { useDraft } from './drafts'
import { useSubmit } from './useSubmit'
import { characterSlots, readPrefill } from './prefill'
import { useShotResults, type ShotResult } from './shotResults'
import { duplicateShot, isAnchorShot, moveShot, newShot, removeShot, videoSequenceShots, type RefReset, type SequenceShot } from './sequence'
import { useCharacterPrefill } from './useCharacterPrefill'

type Flow = 'preview' | 'direct'
type Strategy = 'window' | 'manual' | 'smart'
type RefKind = 'image' | 'video'

interface VideoSequenceDraft {
  shots: SequenceShot[]
  duration: number
  ratio: string
  flow: Flow
  continuity: boolean
  strategy: Strategy
  every: number
  refKind: RefKind
  reference: string[]
  characters: string[]
  project: string
  lastJob: string
  lastShotIds: string[]
}

const INITIAL: VideoSequenceDraft = {
  shots: [newShot(), newShot(), newShot()],
  duration: 5,
  ratio: '16:9',
  flow: 'preview',
  continuity: false,
  strategy: 'window',
  every: 3,
  refKind: 'image',
  reference: [],
  characters: [],
  project: '',
  lastJob: '',
  lastShotIds: [],
}

function RatioOption({ ratio }: { ratio: string }) {
  const [w, h] = ratio.split(':').map(Number)
  const scale = 14 / Math.max(w || 1, h || 1)
  return (
    <span className="inline-flex items-center gap-1">
      {w > 0 && h > 0 && <span aria-hidden className="rounded-[2px] border-[1.5px] border-current" style={{ width: Math.max(4, Math.round(w * scale)), height: Math.max(4, Math.round(h * scale)) }} />}
      {ratio}
    </span>
  )
}

function ClipThumb({ result, label }: { result?: ShotResult; label: string }) {
  const asset = useQuery({ queryKey: keys.assets.detail(result?.assetId ?? ''), queryFn: () => assetsApi.get(result!.assetId!), enabled: Boolean(result?.assetId) })
  return (
    <div className="flex aspect-video w-full items-center justify-center overflow-hidden rounded-thumb border border-dashed border-border-control bg-surface-2 sm:w-40">
      {!result?.assetId ? (
        <Film aria-hidden className="size-6 text-fg-muted" />
      ) : asset.data ? (
        asset.data.thumb_url ? (
          <img src={asset.data.thumb_url} alt={label} className="size-full object-cover" />
        ) : (
          <video src={asset.data.public_url} muted preload="metadata" aria-label={label} className="size-full object-cover" />
        )
      ) : (
        <Skeleton className="size-full" />
      )}
    </div>
  )
}

interface RowProps {
  shots: SequenceShot[]
  index: number
  sentIndex: number | null
  every: number
  duration: number
  manual: boolean
  maxShots: number
  result?: ShotResult
  disabled: boolean
  armed: boolean
  onChange: (next: SequenceShot[], resets?: RefReset[]) => void
  onMove: (to: number) => void
  onArm: () => void
  onDrop: () => void
  onDragEnd: () => void
}

function ShotRow({ shots, index, sentIndex, every, duration, manual, maxShots, result, disabled, armed, onChange, onMove, onArm, onDrop, onDragEnd }: RowProps) {
  const { t } = useTranslation('create')
  const shot = shots[index]
  const n = index + 1
  const title = t('vseq.shot', { n })
  const anchor = sentIndex !== null && isAnchorShot(sentIndex, every)
  const set = (patch: Partial<SequenceShot>) => onChange(shots.map((s) => (s.id === shot.id ? { ...s, ...patch } : s)))

  const onKeyDown = (e: KeyboardEvent) => {
    if (!e.altKey || (e.key !== 'ArrowUp' && e.key !== 'ArrowDown')) return
    e.preventDefault()
    const to = e.key === 'ArrowUp' ? index - 1 : index + 1
    if (to >= 0 && to < shots.length) onMove(to)
  }

  return (
    <li
      aria-label={title}
      draggable={armed}
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        e.preventDefault()
        onDrop()
      }}
      onDragEnd={onDragEnd}
      onKeyDown={onKeyDown}
      className="flex flex-col gap-3 rounded-card border border-border bg-surface p-3 sm:flex-row sm:items-start"
    >
      <div className="flex items-center gap-1 sm:flex-col sm:pt-1">
        <button
          type="button"
          aria-label={t('vseq.handle', { n })}
          title={t('sequence.move.hint')}
          onPointerDown={onArm}
          disabled={disabled}
          className="inline-flex size-8 cursor-grab items-center justify-center rounded-[8px] text-fg-muted hover:bg-surface-2 hover:text-fg active:cursor-grabbing"
        >
          <GripVertical aria-hidden className="size-4" />
        </button>
        <span className="inline-flex size-7 items-center justify-center rounded-full bg-surface-2 text-body font-semibold text-fg tabular-nums">{n}</span>
      </div>
      <ClipThumb result={result} label={title} />
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2 text-caption">
          <h3 className="text-body font-semibold text-fg">{title}</h3>
          <span className="rounded-badge bg-surface-2 px-1.5 py-0.5 text-fg-muted tabular-nums">{t('video.settings.seconds', { n: duration })}</span>
          {sentIndex !== null && <span className={cn('rounded-badge px-1.5 py-0.5', anchor ? 'bg-primary-soft text-primary-text' : 'bg-surface-2 text-fg-muted')}>{anchor ? t('vseq.anchor') : t('vseq.follow')}</span>}
          {result && <StatusPill status={stepPillStatus(result.status)} />}
        </div>
        <Textarea aria-label={title} value={shot.text} maxChars={7000} placeholder={t('vseq.shotPlaceholder')} disabled={disabled} onChange={(e) => set({ text: e.target.value })} className="min-h-20" />
        {manual && anchor && index > 0 && (
          <label className="flex flex-col gap-1 text-caption font-medium text-fg">
            {t('vseq.pick.label')}
            <select
              aria-label={t('vseq.pick.aria', { n })}
              value={shot.ref ?? ''}
              disabled={disabled}
              onChange={(e) => set({ ref: e.target.value || null })}
              className="h-9 w-full rounded-card border border-border-control bg-surface px-2.5 text-body font-normal text-fg sm:max-w-64"
            >
              <option value="">{t('vseq.pick.none')}</option>
              {shots.map((s, i) =>
                i === index ? null : (
                  <option key={s.id} value={s.id} disabled={i > index}>
                    {i < index ? t('vseq.pick.option', { n: i + 1 }) : t('vseq.pick.later', { n: i + 1 })}
                  </option>
                ),
              )}
            </select>
          </label>
        )}
      </div>
      <div className="flex gap-1 sm:flex-col">
        <IconButton size="sm" label={t('sequence.move.up')} icon={<ArrowUp className="size-4" />} disabled={disabled || index === 0} onClick={() => onMove(index - 1)} />
        <IconButton size="sm" label={t('sequence.move.down')} icon={<ArrowDown className="size-4" />} disabled={disabled || index === shots.length - 1} onClick={() => onMove(index + 1)} />
        <IconButton size="sm" label={t('sequence.duplicate')} icon={<Copy className="size-4" />} disabled={disabled || shots.length >= maxShots} onClick={() => onChange(duplicateShot(shots, shot.id))} />
        <IconButton
          size="sm"
          label={t('sequence.remove')}
          icon={<Trash2 className="size-4" />}
          disabled={disabled || shots.length <= 1}
          onClick={() => {
            const r = removeShot(shots, shot.id)
            onChange(r.shots, r.resets)
          }}
        />
      </div>
    </li>
  )
}

/** Video sequence (spec P04): a storyboard generated as previews first by default. */
export default function VideoSequenceStudio() {
  const { t, i18n } = useTranslation('create')
  const location = useLocation()
  const navigate = useNavigate()
  const me = useMe()
  const [currentProject] = useCurrentProject(me.data?.biz_id)
  const [draft, update] = useDraft<VideoSequenceDraft>(me.data?.biz_id, 'video-sequence', INITIAL)
  useCharacterPrefill(draft.characters, (characters) => update({ characters }))
  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities, staleTime: 60_000 })
  const characters = useQuery({ queryKey: keys.characters, queryFn: createApi.characters })
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list })
  const [resets, setResets] = useState<RefReset[]>([])
  const [moved, setMoved] = useState('')
  const [armed, setArmed] = useState<string | null>(null)
  const dragFrom = useRef<number | null>(null)

  // "Turn into a sequence" from a comic, or create again from a sequence task, seeds the draft once.
  useEffect(() => {
    const state = location.state as { prefillSuggestion?: { kind?: string; shots?: string[] } } | null
    const suggestion = state?.prefillSuggestion
    const p = readPrefill(location.state, 'video.sequence')
    const texts = suggestion?.kind === 'to-sequence' ? suggestion.shots : p?.shots
    if (!texts?.length) return
    update({ shots: texts.map((text) => newShot(text)), ...(p?.characters ? { characters: p.characters } : {}) })
    setResets([])
    navigate('.', { replace: true, state: {} })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state])

  const video = caps.data?.video
  const maxShots = video?.sequence_max_shots ?? 12
  const durations = video ? Array.from({ length: video.duration_max - video.duration_min + 1 }, (_, i) => video.duration_min + i) : [draft.duration]
  const shots = draft.shots.length ? draft.shots : INITIAL.shots
  const spec = videoSequenceShots(shots)
  const count = spec.shots.length
  const total = count * draft.duration
  const project = draft.project || currentProject || ''
  const manual = draft.continuity && draft.strategy === 'manual'
  const sentIndex = new Map(shots.filter((s) => s.text.trim()).map((s, i) => [s.id, i]))
  const { results, job: lastJob } = useShotResults(draft.lastJob, draft.lastShotIds)

  const setShots = (next: SequenceShot[], changed: RefReset[] = []) => {
    update({ shots: next })
    setResets(changed)
  }
  const move = (from: number, to: number) => {
    const r = moveShot(shots, from, to)
    setShots(r.shots, r.resets)
    setMoved(t('vseq.moved', { n: to + 1 }))
  }

  const specFor = (flow: Flow) => ({
    shots: spec.shots,
    ...(manual && spec.shot_reference_overrides ? { shot_reference_overrides: spec.shot_reference_overrides } : {}),
    duration_seconds: draft.duration,
    ratio: draft.ratio,
    ...(flow === 'direct' ? { skip_preview: true } : {}),
    ...(draft.continuity ? { narrative_continuity: true, reference_selection_mode: draft.strategy } : {}),
    ...(draft.every !== 3 ? { recalibrate_every: draft.every } : {}),
    ...(draft.reference[0] ? { [draft.refKind === 'video' ? 'source_video_asset_id' : 'source_image_asset_id']: draft.reference[0] } : {}),
    ...(draft.characters.length ? { characters: characterSlots(draft.characters) } : {}),
  })
  const valid = count > 0 && count <= maxShots
  const request: CreateRequest | null = valid ? { workflow_name: 'video.sequence', spec: specFor(draft.flow), ...(project ? { project_id: project } : {}) } : null
  const sentIds = shots.filter((s) => s.text.trim()).map((s) => s.id)
  const job = useSubmit(request, (bizId) => update({ lastJob: bizId, lastShotIds: sentIds }))

  // The other flow's price, so both choices show what they cost.
  const other: Flow = draft.flow === 'preview' ? 'direct' : 'preview'
  const otherBody = valid ? JSON.stringify({ workflow_name: 'video.sequence', spec: specFor(other) }) : ''
  const otherDebounced = useDebouncedValue(otherBody, 300)
  const otherEstimate = useQuery({
    queryKey: keys.estimate(otherDebounced),
    queryFn: ({ signal }) => createApi.estimate(JSON.parse(otherDebounced) as CreateRequest, signal),
    enabled: Boolean(otherDebounced),
    retry: false,
    staleTime: 30_000,
  })
  const price = (flow: Flow) => {
    const credits = flow === draft.flow ? job.estimate?.data?.credits_total : otherEstimate.data?.credits_total
    return credits === undefined ? t('vseq.flow.estimatePending') : t('vseq.flow.estimate', { n: formatNumber(credits, i18n.language) })
  }

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6">
      <CreateNav mode="video-sequence" />
      <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_400px]">
        <div className="flex min-w-0 flex-col gap-4">
          <header className="flex flex-wrap items-end justify-between gap-3">
            <div className="min-w-0">
              <h1 className="text-title font-semibold text-fg">{t('vseq.title')}</h1>
              <p className="text-body text-fg-muted">{t('vseq.description')}</p>
            </div>
            <Button icon={<Plus aria-hidden className="size-4" />} disabled={shots.length >= maxShots || job.submitting} onClick={() => setShots([...shots, newShot()])}>
              {shots.length >= maxShots ? t('vseq.max', { max: maxShots }) : t('vseq.add')}
            </Button>
          </header>

          {lastJob?.status === 'awaiting_review' && (
            <div role="status" className="flex flex-wrap items-center gap-3 rounded-card border border-warning bg-warning-soft p-3 text-body text-warning-fg">
              <span className="flex-1">{t('vseq.review.ready')}</span>
              <Link to={`/jobs/${draft.lastJob}`} className={buttonClasses('primary', 'sm')}>
                {t('vseq.review.go')}
              </Link>
            </div>
          )}
          {resets.length > 0 && (
            <div role="status" className="flex gap-3 rounded-card border border-warning bg-warning-soft p-3 text-body text-warning-fg">
              <TriangleAlert aria-hidden className="mt-0.5 size-5 shrink-0 text-warning" />
              <ul className="flex-1">
                {resets.map((r) => (
                  <li key={`${r.shot}-${r.ref}`}>{t(`vseq.reset.${r.reason}`, { shot: r.shot, ref: r.ref })}</li>
                ))}
              </ul>
              <Button size="sm" variant="ghost" onClick={() => setResets([])}>
                {t('sequence.reset.dismiss')}
              </Button>
            </div>
          )}
          <p className="text-caption text-fg-muted">{t('vseq.anchorHint')}</p>
          <p aria-live="polite" className="sr-only">
            {moved}
          </p>
          <ol className="flex flex-col gap-3">
            {shots.map((s, i) => (
              <ShotRow
                key={s.id}
                shots={shots}
                index={i}
                sentIndex={sentIndex.get(s.id) ?? null}
                every={draft.every}
                duration={draft.duration}
                manual={manual}
                maxShots={maxShots}
                result={results[s.id]}
                disabled={job.submitting}
                armed={armed === s.id}
                onChange={setShots}
                onMove={(to) => move(i, to)}
                onArm={() => {
                  setArmed(s.id)
                  dragFrom.current = i
                }}
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
          </ol>
        </div>

        <Card padding="none" className="flex flex-col gap-5 p-4 lg:p-5 xl:sticky xl:top-4">
          <h2 className="text-section font-semibold text-fg">{t('vseq.settings.title')}</h2>
          <div className="grid gap-4 sm:grid-cols-2 [&>*]:min-w-0">
            <Field label={t('vseq.settings.perShot')}>
              <Select value={String(draft.duration)} onChange={(e) => update({ duration: Number(e.target.value) })}>
                {durations.map((d) => (
                  <option key={d} value={d}>
                    {t('video.settings.seconds', { n: d })}
                  </option>
                ))}
              </Select>
            </Field>
            <div className="flex flex-col gap-1.5">
              <span className="text-label font-medium text-fg">{t('vseq.settings.total')}</span>
              <p className="flex h-10 items-center text-body font-semibold text-fg tabular-nums">{t('vseq.settings.totalValue', { total, n: count, d: draft.duration })}</p>
            </div>
          </div>
          <div className="flex flex-col gap-1.5">
            <span className="text-label font-medium text-fg">{t('vseq.settings.ratio')}</span>
            <SegmentedControl
              fullWidth
              label={t('vseq.settings.ratio')}
              value={draft.ratio}
              onChange={(v) => update({ ratio: v })}
              options={(video?.ratios ?? ['16:9']).map((r) => ({ value: r, label: <RatioOption ratio={r} /> }))}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <CharacterPicker characters={characters.data?.characters ?? []} value={draft.characters} onChange={(ids) => update({ characters: ids })} />
            <p className="text-caption text-fg-muted">{t('vseq.settings.anchorHelp')}</p>
          </div>
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between gap-3">
              <span className="text-label font-medium text-fg">{t('vseq.settings.reference')}</span>
              <SegmentedControl<RefKind>
                label={t('vseq.settings.reference')}
                value={draft.refKind}
                onChange={(v) => update({ refKind: v, reference: [] })}
                options={[
                  { value: 'image', label: t('vseq.settings.referenceImage') },
                  { value: 'video', label: t('vseq.settings.referenceVideo') },
                ]}
              />
            </div>
            <ReferencePicker key={draft.refKind} kind={draft.refKind} label={draft.refKind === 'video' ? t('vseq.settings.referenceVideo') : t('vseq.settings.referenceImage')} value={draft.reference} onChange={(ids) => update({ reference: ids.slice(-1) })} limits={{ max: 1 }} />
          </div>

          <ChoiceCards<Flow>
            columns={1}
            label={t('vseq.flow.label')}
            value={draft.flow}
            onChange={(f) => update({ flow: f })}
            choices={[
              { value: 'preview', title: `${t('vseq.flow.preview.title')} · ${t('vseq.flow.preview.badge')}`, description: t('vseq.flow.preview.body'), meta: price('preview') },
              { value: 'direct', title: t('vseq.flow.direct.title'), description: t('vseq.flow.direct.body'), meta: price('direct') },
            ]}
          />

          <details className="group rounded-card border border-border">
            <summary className="flex h-11 cursor-pointer list-none items-center justify-between px-3 text-body font-semibold text-fg">
              {t('vseq.advanced.title')}
              <ChevronDown aria-hidden className="size-4 text-fg-muted transition-transform group-open:rotate-180" />
            </summary>
            <div className="flex flex-col gap-4 border-t border-border p-3">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <span className="text-label font-medium text-fg">{t('vseq.advanced.continuity')}</span>
                  <p className="text-caption text-fg-muted">{t('vseq.advanced.continuityHelp')}</p>
                </div>
                <Switch label={t('vseq.advanced.continuity')} checked={draft.continuity} onChange={(on) => update({ continuity: on })} />
              </div>
              <div className="flex flex-col gap-1.5">
                <span className="text-label font-medium text-fg">{t('vseq.advanced.strategy')}</span>
                <SegmentedControl<Strategy>
                  fullWidth
                  label={t('vseq.advanced.strategy')}
                  value={draft.strategy}
                  onChange={(v) => update({ strategy: v })}
                  options={(['window', 'manual', 'smart'] as const).map((v) => ({ value: v, label: t(`vseq.advanced.${v}`), disabled: !draft.continuity }))}
                />
                <p className="text-caption text-fg-muted">{t(`vseq.advanced.strategyHelp.${draft.strategy}`)}</p>
              </div>
              <Field label={t('vseq.advanced.every')}>
                <Select value={String(draft.every)} onChange={(e) => update({ every: Number(e.target.value) })}>
                  {[2, 3, 4, 5, 6].map((n) => (
                    <option key={n} value={n}>
                      {t('vseq.advanced.everyValue', { n })}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>
          </details>

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

          <CostBar
            ready={Boolean(request)}
            credits={job.estimate?.data?.credits_total}
            calculating={Boolean(request) && job.stale}
            failed={Boolean(job.estimate?.isError)}
              reason={job.estimateError}
            detail={t('vseq.detail', { n: count, total })}
            notice={job.notice}
            actionLabel={draft.flow === 'direct' ? t('vseq.action.direct', { n: count }) : t('vseq.action.preview', { n: count })}
            onAction={job.submit}
            loading={job.submitting}
            disabled={!job.canSubmit}
          />
        </Card>
      </div>
    </div>
  )
}
