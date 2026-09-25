import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { ArrowRight, Clapperboard, ImageIcon } from 'lucide-react'
import { Card, ConfirmDialog, Field, SegmentedControl, Select, Skeleton, Switch, TabList, TabPanel, Tabs } from '../../ui'
import { createApi, type CreateRequest } from '../../lib/api/create'
import { assetsApi } from '../../lib/api/assets'
import { projectsApi } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { formatNumber } from '../../lib/format'
import { useMe } from '../../app/useMe'
import { useCurrentProject } from '../../app/currentProject'
import { CreateNav } from './CreateNav'
import { PromptInput } from './PromptInput'
import { ReferencePicker } from './ReferencePicker'
import { CharacterPicker } from './CharacterPicker'
import { CostBar } from './CostBar'
import { ResultPane } from './ResultPane'
import { useDraft } from './drafts'
import { useSubmit } from './useSubmit'
import { characterSlots, readPrefill } from './prefill'
import { clearedBySwitch, mediaForMode, missingInput, modeOfSpec, videoSpec, type VideoMedia, type VideoMode } from './video'

type RefTab = 'images' | 'videos' | 'audios'

interface VideoDraft extends VideoMedia {
  mode: VideoMode
  text: string
  duration: number
  resolution: string
  ratio: string
  enhance: boolean
  refTab: RefTab
  characters: string[]
  project: string
  lastJob: string
}

const INITIAL: VideoDraft = {
  mode: 'text', text: '', duration: 6, resolution: '768P', ratio: '16:9', enhance: false, refTab: 'images',
  first: [], last: [], images: [], videos: [], audios: [], characters: [], project: '', lastJob: '',
}

// No provider limit is published for reference images and audio; these keep the picker bounded.
const MAX_REFERENCE_IMAGES = 9
const MAX_REFERENCE_AUDIOS = 3

/** A ratio drawn as its shape. */
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

function FrameStill({ id, label, empty }: { id?: string; label: string; empty: string }) {
  const asset = useQuery({ queryKey: keys.assets.detail(id ?? ''), queryFn: () => assetsApi.get(id!), enabled: Boolean(id) })
  return (
    <figure className="flex min-w-0 flex-1 flex-col gap-1.5">
      <div className="flex aspect-video items-center justify-center overflow-hidden rounded-card border border-border bg-surface-2">
        {!id ? (
          <span className="flex flex-col items-center gap-1 text-caption text-fg-muted">
            <ImageIcon aria-hidden className="size-6" />
            {empty}
          </span>
        ) : asset.data ? (
          <img src={asset.data.public_url} alt={label} className="size-full object-contain" />
        ) : (
          <Skeleton className="size-full" />
        )}
      </div>
      <figcaption className="text-caption text-fg-muted">{label}</figcaption>
    </figure>
  )
}

/** Single video (spec P03): text, first and last frame, or reference media. */
export default function VideoStudio() {
  const { t, i18n } = useTranslation('create')
  const location = useLocation()
  const navigate = useNavigate()
  const me = useMe()
  const [currentProject] = useCurrentProject(me.data?.biz_id)
  const [draft, update] = useDraft<VideoDraft>(me.data?.biz_id, 'video', INITIAL)
  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities, staleTime: 60_000 })
  const characters = useQuery({ queryKey: keys.characters, queryFn: createApi.characters })
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list })
  const [pendingMode, setPendingMode] = useState<VideoMode | null>(null)

  // A prefill (create again, or "turn into a video" from an image) seeds the draft once.
  useEffect(() => {
    const state = location.state as { prefillSuggestion?: { kind?: string; sourceAssetId?: string } } | null
    const suggestion = state?.prefillSuggestion
    const p = readPrefill(location.state, 'video.single')
    if (suggestion?.kind === 'to-video' && suggestion.sourceAssetId) {
      update({ mode: 'frames', ...mediaForMode({ ...INITIAL, first: [suggestion.sourceAssetId] }, 'frames') })
    } else if (p) {
      const v = p.video ?? {}
      const mode = modeOfSpec(v)
      update({
        mode,
        text: p.text ?? '',
        characters: p.characters ?? [],
        duration: v.duration_seconds ?? INITIAL.duration,
        resolution: v.resolution ?? INITIAL.resolution,
        ratio: v.ratio ?? INITIAL.ratio,
        enhance: Boolean(v.prompt_enhance),
        first: v.first_frame_asset_id ? [v.first_frame_asset_id] : [],
        last: v.last_frame_asset_id ? [v.last_frame_asset_id] : [],
        images: v.reference_image_asset_ids ?? [],
        videos: v.reference_video_asset_ids ?? [],
        audios: v.reference_audio_asset_ids ?? [],
      })
    } else {
      return
    }
    navigate('.', { replace: true, state: {} })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state])

  const video = caps.data?.video
  const durations = video ? Array.from({ length: video.duration_max - video.duration_min + 1 }, (_, i) => video.duration_min + i) : [draft.duration]
  const maxChars = video?.max_prompt_chars ?? 7000
  const maxVideos = video?.max_reference_videos ?? 3
  const project = draft.project || currentProject || ''
  const tooLong = [...draft.text].length > maxChars
  const missing = missingInput(draft.mode, draft, draft.text)
  const request: CreateRequest | null =
    !missing && !tooLong
      ? {
          workflow_name: 'video.single',
          spec: {
            ...videoSpec(draft.mode, draft, draft),
            ...(draft.characters.length ? { characters: characterSlots(draft.characters) } : {}),
          },
          ...(project ? { project_id: project } : {}),
        }
      : null
  const job = useSubmit(request, (bizId) => update({ lastJob: bizId }))
  const enhanceCost = job.estimate?.data?.items.find((i) => i.kind === 'prompt_enhance')?.credits

  const switchTo = (next: VideoMode) => {
    if (next === draft.mode) return
    if (clearedBySwitch(draft, next).length > 0) setPendingMode(next)
    else update({ mode: next, ...mediaForMode(draft, next) })
  }
  const cleared = pendingMode ? clearedBySwitch(draft, pendingMode) : []

  const refTabs = (['images', 'videos', 'audios'] as const).map((k) => ({ value: k, label: t(`video.refs.${k}`), ...(draft[k].length ? { count: draft[k].length } : {}) }))

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6">
      <CreateNav mode="video" />
      <div className="grid items-start gap-6 xl:grid-cols-[460px_minmax(0,1fr)]">
        <Card padding="none" className="flex flex-col gap-5 p-4 lg:p-5">
          <header className="flex items-start gap-3">
            <span className="inline-flex size-10 shrink-0 items-center justify-center rounded-card bg-primary-soft text-primary-text">
              <Clapperboard aria-hidden className="size-5" />
            </span>
            <div className="min-w-0">
              <h1 className="text-section font-semibold text-fg">{t('video.title')}</h1>
              <p className="text-caption text-fg-muted">{t('video.description')}</p>
            </div>
          </header>

          <div className="flex flex-col gap-1.5">
            <SegmentedControl<VideoMode>
              fullWidth
              label={t('video.mode.label')}
              value={draft.mode}
              onChange={switchTo}
              options={[
                { value: 'text', label: t('video.mode.text') },
                { value: 'frames', label: t('video.mode.frames') },
                { value: 'refs', label: t('video.mode.refs') },
              ]}
            />
            <p className="text-caption text-fg-muted">{t(`video.mode.hint.${draft.mode}`)}</p>
          </div>

          <PromptInput
            label={t('video.prompt.label')}
            value={draft.text}
            onChange={(v) => update({ text: v })}
            maxChars={maxChars}
            characters={characters.data?.characters ?? []}
            onMentionCharacter={(c) => !draft.characters.includes(c.biz_id) && update({ characters: [...draft.characters, c.biz_id].slice(0, 6) })}
            error={tooLong ? t('prompt.tooLong', { max: maxChars }) : undefined}
          />

          {draft.mode === 'frames' && (
            <div className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-start gap-2">
              <ReferencePicker label={t('video.frames.first')} required value={draft.first} onChange={(ids) => update({ first: ids.slice(-1) })} limits={{ max: 1 }} />
              <ArrowRight aria-hidden className="mt-14 size-5 text-fg-muted" />
              <ReferencePicker label={t('video.frames.last')} value={draft.last} onChange={(ids) => update({ last: ids.slice(-1) })} limits={{ max: 1 }} />
            </div>
          )}
          {draft.mode === 'refs' && (
            <div className="flex flex-col gap-2">
              <span className="text-label font-medium text-fg">{t('video.refs.label')}</span>
              <Tabs value={draft.refTab} onValueChange={(v) => update({ refTab: v as RefTab })}>
                <TabList label={t('video.refs.label')} items={refTabs} />
                <TabPanel value="images" className="pt-3">
                  <ReferencePicker label={t('video.refs.images')} value={draft.images} onChange={(ids) => update({ images: ids })} limits={{ max: MAX_REFERENCE_IMAGES }} />
                </TabPanel>
                <TabPanel value="videos" className="flex flex-col gap-2 pt-3">
                  <ReferencePicker kind="video" label={t('video.refs.videos')} value={draft.videos} onChange={(ids) => update({ videos: ids })} limits={{ max: maxVideos }} />
                  <p className="text-caption text-fg-muted">{t('video.refs.videoBudget', { max: maxVideos, seconds: video?.reference_video_max_seconds ?? 15 })}</p>
                </TabPanel>
                <TabPanel value="audios" className="pt-3">
                  <ReferencePicker kind="audio" label={t('video.refs.audios')} value={draft.audios} onChange={(ids) => update({ audios: ids })} limits={{ max: MAX_REFERENCE_AUDIOS }} />
                </TabPanel>
              </Tabs>
            </div>
          )}

          <section aria-labelledby="video-settings" className="flex flex-col gap-4">
            <h2 id="video-settings" className="text-label font-semibold text-fg">
              {t('video.settings.title')}
            </h2>
            <div className="grid gap-4 sm:grid-cols-2 [&>*]:min-w-0">
              <Field label={t('video.settings.duration')}>
                <Select value={String(draft.duration)} onChange={(e) => update({ duration: Number(e.target.value) })}>
                  {durations.map((d) => (
                    <option key={d} value={d}>
                      {t('video.settings.seconds', { n: d })}
                    </option>
                  ))}
                </Select>
              </Field>
              <div className="flex flex-col gap-1.5">
                <span className="text-label font-medium text-fg">{t('video.settings.resolution')}</span>
                <SegmentedControl
                  fullWidth
                  label={t('video.settings.resolution')}
                  value={draft.resolution}
                  onChange={(v) => update({ resolution: v })}
                  options={(video?.resolutions ?? ['768P', '2K']).map((r) => ({ value: r, label: r }))}
                />
              </div>
            </div>
            <div className="flex flex-col gap-1.5">
              <span className="text-label font-medium text-fg">{t('video.settings.ratio')}</span>
              {draft.mode === 'text' ? (
                <SegmentedControl
                  fullWidth
                  label={t('video.settings.ratio')}
                  value={draft.ratio}
                  onChange={(v) => update({ ratio: v })}
                  options={(video?.ratios ?? ['16:9']).map((r) => ({ value: r, label: <RatioOption ratio={r} /> }))}
                />
              ) : (
                <>
                  <p className="flex h-9 items-center rounded-card border border-border bg-surface-2 px-3 text-body text-fg-muted">{t('video.settings.ratioFollows')}</p>
                  <p className="text-caption text-fg-muted">{t('video.settings.ratioFollowsHint')}</p>
                </>
              )}
            </div>
          </section>

          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <label htmlFor="video-enhance" className="text-label font-medium text-fg">
                {t('video.enhance.label')}
              </label>
              <p className="text-caption text-fg-muted">
                {t('video.enhance.help')}{' '}
                {draft.enhance && enhanceCost !== undefined ? t('video.enhance.cost', { n: formatNumber(enhanceCost, i18n.language) }) : t('video.enhance.costPending')}
              </p>
            </div>
            <Switch id="video-enhance" label={t('video.enhance.label')} checked={draft.enhance} onChange={(on) => update({ enhance: on })} />
          </div>

          <div className="grid gap-4 sm:grid-cols-2 [&>*]:min-w-0">
            <CharacterPicker characters={characters.data?.characters ?? []} value={draft.characters} onChange={(ids) => update({ characters: ids })} />
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
          </div>

          {missing && missing !== 'text' && (
            <p className="text-caption text-warning-fg">{missing === 'firstFrame' ? t('video.frames.firstRequired') : t('video.refs.required')}</p>
          )}
          <CostBar
            ready={Boolean(request)}
            credits={job.estimate?.data?.credits_total}
            calculating={Boolean(request) && job.stale}
            failed={Boolean(job.estimate?.isError)}
            notice={job.notice}
            actionLabel={t('video.generate')}
            onAction={job.submit}
            loading={job.submitting}
            disabled={!job.canSubmit}
          />
        </Card>

        <ResultPane
          jobId={draft.lastJob || null}
          empty={
            draft.mode === 'frames' ? (
              <Card padding="md" className="flex flex-col gap-3">
                <h3 className="text-body font-semibold text-fg">{t('video.preview.title')}</h3>
                <div className="flex items-center gap-3">
                  <FrameStill id={draft.first[0]} label={t('video.preview.first')} empty={t('video.preview.empty')} />
                  <ArrowRight aria-hidden className="size-5 shrink-0 text-fg-muted" />
                  <FrameStill id={draft.last[0]} label={t('video.preview.last')} empty={t('video.preview.noLast')} />
                </div>
              </Card>
            ) : undefined
          }
        />
      </div>

      <ConfirmDialog
        open={pendingMode !== null}
        onOpenChange={(o) => !o && setPendingMode(null)}
        title={pendingMode ? t('video.switch.title', { mode: t(`video.mode.${pendingMode}`) }) : ''}
        body={
          <>
            <p>{t('video.switch.body')}</p>
            <ul className="list-disc pl-5">
              {cleared.map((c) => (
                <li key={c.key}>{t(`video.switch.item.${c.key}`, { count: c.count })}</li>
              ))}
            </ul>
          </>
        }
        confirmLabel={t('video.switch.confirm')}
        onConfirm={() => {
          if (pendingMode) update({ mode: pendingMode, ...mediaForMode(draft, pendingMode) })
          setPendingMode(null)
        }}
      />
    </div>
  )
}
