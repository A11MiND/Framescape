import { useEffect, useState, type ReactNode } from 'react'
import { useMutation, useQuery, keepPreviousData } from '@tanstack/react-query'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { DndContext, closestCenter, PointerSensor, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, verticalListSortingStrategy, arrayMove, useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { api, ApiError, type Spec, type WorkflowName, type JobResponse } from '../lib/api'
import {
  estimateImageCredits,
  estimateVideoCredits,
  estimatePromptEnhanceCredits,
  estimateStorySplitCredits,
} from '../lib/pricing'
import { videoSingleSchema, RATIO_VALUES } from '../lib/videoSpec'
import { resultAssetIds, WORKFLOW_LABEL_KEY, type Tab } from '../lib/jobResult'
import { displayNodeError, firstSpecificError } from '../lib/errors'
import { suggestActions, type SuggestedAction } from '../lib/suggestions'
import { shotMode, SHOT_MODE_LABEL_KEY, SHOT_MODE_CLASS, type ShotMode } from '../lib/shotPlan'
import { useToast } from '../components/Toast'
import AppShell from '../components/AppShell'
import { AssetPicker } from '../components/AssetPicker'
import PreviewGate from '../components/PreviewGate'
import GenerationProgress from '../components/GenerationProgress'
import PresetCarousel from '../components/PresetCarousel'
import AnimatedNumber from '../components/AnimatedNumber'
import { useAuthStore } from '../lib/authStore'
import { getDeviceId } from '../lib/deviceId'
import { useJobStream } from '../hooks/useJobStream'
import { useDebouncedValue } from '../hooks/useDebouncedValue'

// PRD §19.4.1's full creation studio ("工坊"), now mounted at `/` per
// §19.3 instead of behind a login wall — this is the single biggest
// information-architecture gap the original jimeng-comparison surfaced: the
// old build redirected `/` straight to `/login`, so the anonymous trial
// (F1.2, POST /trial/image, real endpoint since W1) was only reachable from
// a card buried on the login screen. Guests now land here directly;
// anything that needs an authed GET (characters/presets/me) is simply not
// fetched (`enabled: !isGuest`) rather than erroring, and the composer
// degrades to "compose + one free trial" instead of vanishing.
//
// Structural change from the tab-sidebar build: six workflows collapse into
// one composer (headline dropdown + quick-switch cards drive the same `tab`
// state) with a capsule parameter row, instead of six parallel forms behind
// six sidebar tabs. Every Spec field the old build could set, this one
// still can — see the blueprint's capsule → Spec field table.

const TAB_META_KEY: Record<Tab, { icon: string; blurbKey: string }> = {
  'image.single': { icon: '🖼', blurbKey: 'studio.tabMeta.imageSingle' },
  'image.batch': { icon: '▦', blurbKey: 'studio.tabMeta.imageBatch' },
  'image.comic4': { icon: '🗯', blurbKey: 'studio.tabMeta.imageComic4' },
  'image.sequence': { icon: '⛓', blurbKey: 'studio.tabMeta.imageSequence' },
  'video.single': { icon: '🎬', blurbKey: 'studio.tabMeta.videoSingle' },
  'video.sequence': { icon: '🎞', blurbKey: 'studio.tabMeta.videoSequence' },
}
const TABS = Object.keys(TAB_META_KEY) as Tab[]

// video.sequence's shots need a stable identity per row for dnd-kit's
// drag-reorder (array index isn't stable across a reorder) — this is the
// one shape difference from every other tab's plain string[] shot list.
interface ShotItem {
  id: string
  text: string
}
function toShotItems(texts: string[]): ShotItem[] {
  return (texts.length ? texts : ['']).map((text) => ({ id: crypto.randomUUID(), text }))
}

// F6.5's "双保险": videoSpec.ts's zod schema is still the structural second
// guard checked right before submit — this is the first guard, and it's now
// enforced by construction (only one panel can ever be mounted) instead of
// by graying out whichever panel lost the race, which is what a single
// "全能参考" dropdown gets right that two parallel opacity-40 panels don't:
// there's no state where the user has to read a tooltip to find out why
// something is disabled.
type RefMode = 'none' | 'firstLast' | 'reference'

function useCharacters(enabled: boolean) {
  return useQuery({ queryKey: ['characters'], queryFn: api.listCharacters, enabled })
}
function usePresets(enabled: boolean) {
  return useQuery({ queryKey: ['presets'], queryFn: () => api.listPresets(), enabled })
}
function useMe(enabled: boolean) {
  return useQuery({ queryKey: ['me'], queryFn: api.me, enabled })
}

export default function Studio() {
  const { t } = useTranslation()
  const accessToken = useAuthStore((s) => s.accessToken)
  const isGuest = !accessToken
  const navigate = useNavigate()
  const location = useLocation()

  const [tab, setTab] = useState<Tab>('image.single')
  const [text, setText] = useState(t('studio.examples.fox'))
  const [n, setN] = useState(4)
  const [panels, setPanels] = useState(['', '', '', ''])
  const [shots, setShots] = useState([''])
  const [slotA, setSlotA] = useState('')
  const [slotB, setSlotB] = useState('')
  const [presetIds, setPresetIds] = useState<string[]>([])
  const [bizId, setBizId] = useState<string | null>(null)

  // video.single-only state (F6.1-F6.5).
  const [vText, setVText] = useState(t('studio.examples.foxVideo'))
  const [duration, setDuration] = useState(5)
  const [resolution, setResolution] = useState<'768P' | '2K'>('768P')
  const [ratio, setRatio] = useState<(typeof RATIO_VALUES)[number]>('16:9')
  const [refMode, setRefMode] = useState<RefMode>('none')
  const [firstFrameAssetId, setFirstFrameAssetId] = useState('')
  const [lastFrameAssetId, setLastFrameAssetId] = useState('')
  const [refImageIds, setRefImageIds] = useState<string[]>([])
  const [refVideoIds, setRefVideoIds] = useState<string[]>([])
  const [promptEnhance, setPromptEnhance] = useState(false)
  // image.single-only state (F5.8): optional image-to-image source.
  const [sourceImageId, setSourceImageId] = useState('')
  // image.comic4-only state (F5.4).
  const [comicMode, setComicMode] = useState<'manual' | 'auto'>('manual')
  const [story, setStory] = useState('')

  // video.sequence-only state (F6.7/F6.8).
  const [vsShots, setVsShots] = useState<ShotItem[]>(() => toShotItems(['']))
  const [vsDuration, setVsDuration] = useState(5)
  const [vsRatio, setVsRatio] = useState<(typeof RATIO_VALUES)[number]>('16:9')
  const [vsRecalibrateEvery, setVsRecalibrateEvery] = useState(3)

  // F2.5's "以此再生成": AssetDetail navigates here with the source job's
  // exact workflow_name/spec in router state — buildSpec()'s inverse,
  // mapping that Spec back onto every piece of local state it came from.
  // Guarded to run once per navigation (not on every render): it's meant
  // to seed the form, not keep clobbering whatever the user types next.
  useEffect(() => {
    const prefill = (location.state as { prefillJob?: { workflowName: Tab; spec: Spec } } | null)?.prefillJob
    if (!prefill) return
    const { workflowName, spec } = prefill
    setTab(workflowName)
    setBizId(null)

    setSlotA(spec.characters?.find((c) => c.slot === 'A')?.character_id ?? '')
    setSlotB(spec.characters?.find((c) => c.slot === 'B')?.character_id ?? '')
    setPresetIds(spec.preset_ids ?? [])

    if (workflowName === 'image.single' || workflowName === 'image.batch') {
      setText(spec.text ?? '')
      if (spec.n) setN(spec.n)
      setSourceImageId(spec.source_image_asset_id ?? '')
    } else if (workflowName === 'image.comic4') {
      if (spec.panels?.length === 4) {
        setComicMode('manual')
        setPanels(spec.panels)
      } else if (spec.story) {
        setComicMode('auto')
        setStory(spec.story)
      }
    } else if (workflowName === 'image.sequence') {
      setShots(spec.shots?.length ? spec.shots : [''])
    } else if (workflowName === 'video.single') {
      setVText(spec.text ?? '')
      if (spec.duration_seconds) setDuration(spec.duration_seconds)
      if (spec.resolution === '768P' || spec.resolution === '2K') setResolution(spec.resolution)
      if (spec.ratio) setRatio(spec.ratio as (typeof RATIO_VALUES)[number])
      if (spec.first_frame_asset_id || spec.last_frame_asset_id) {
        setRefMode('firstLast')
        setFirstFrameAssetId(spec.first_frame_asset_id ?? '')
        setLastFrameAssetId(spec.last_frame_asset_id ?? '')
      } else if (spec.reference_image_asset_ids?.length || spec.reference_video_asset_ids?.length) {
        setRefMode('reference')
        setRefImageIds(spec.reference_image_asset_ids ?? [])
        setRefVideoIds(spec.reference_video_asset_ids ?? [])
      } else {
        setRefMode('none')
      }
      setPromptEnhance(!!spec.prompt_enhance)
    } else if (workflowName === 'video.sequence') {
      setVsShots(toShotItems(spec.shots ?? []))
      if (spec.duration_seconds) setVsDuration(spec.duration_seconds)
      if (spec.ratio) setVsRatio(spec.ratio as (typeof RATIO_VALUES)[number])
      if (spec.recalibrate_every) setVsRecalibrateEvery(spec.recalibrate_every)
    }

    // Clear the router state so refreshing or navigating back here later
    // doesn't silently re-apply a stale prefill over new edits.
    navigate('.', { replace: true, state: {} })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state])

  const me = useMe(!isGuest)
  const characters = useCharacters(!isGuest)
  const presets = usePresets(!isGuest)
  const pushToast = useToast()
  // §19.4.4's shot drag-reorder. A small activation distance keeps a plain
  // click on the drag handle from being misread as a drag when the pointer
  // moves a pixel or two before release.
  const dndSensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  // F1.2's anonymous trial — relocated here from the login page ("give
  // value before the wall" only works if the value is visible before the
  // wall, not after it). Self-contained, doesn't touch jobs/credits/assets.
  const [trialPrompt, setTrialPrompt] = useState(t('studio.examples.rooftop'))
  const [trialImageUrl, setTrialImageUrl] = useState<string | null>(null)
  const [trialError, setTrialError] = useState<string | null>(null)
  const [trialBusy, setTrialBusy] = useState(false)

  async function runTrial() {
    setTrialBusy(true)
    setTrialError(null)
    try {
      const res = await api.trialImage(trialPrompt, getDeviceId())
      setTrialImageUrl(res.image_url)
    } catch (err) {
      setTrialError(err instanceof ApiError ? err.message : t('studio.errors.trialFailed'))
    } finally {
      setTrialBusy(false)
    }
  }

  function setRefModeAndClear(mode: RefMode) {
    setRefMode(mode)
    if (mode !== 'firstLast') {
      setFirstFrameAssetId('')
      setLastFrameAssetId('')
    }
    if (mode !== 'reference') {
      setRefImageIds([])
      setRefVideoIds([])
    }
  }

  const videoValidation = videoSingleSchema.safeParse({
    text: vText,
    duration,
    resolution,
    ratio,
    firstFrameAssetId,
    lastFrameAssetId,
    referenceImageAssetIds: refImageIds,
    referenceVideoAssetIds: refVideoIds,
  })

  // Shared by the mutation and the estimate query below — kept as one
  // function so the two can never build a different Spec for what looks to
  // the user like "the same submission," not two separate copies of this
  // switch that could quietly drift apart.
  function buildSpec(): Spec {
    const characterSlots = [
      slotA && { slot: 'A', character_id: slotA },
      slotB && { slot: 'B', character_id: slotB },
    ].filter(Boolean) as Spec['characters']

    const spec: Spec = {
      characters: characterSlots?.length ? characterSlots : undefined,
      preset_ids: presetIds.length ? presetIds : undefined,
    }
    if (tab === 'image.single' || tab === 'image.batch') {
      spec.text = text
      if (tab === 'image.batch') spec.n = n
      if (tab === 'image.single' && sourceImageId) spec.source_image_asset_id = sourceImageId
    } else if (tab === 'image.comic4') {
      if (comicMode === 'auto') {
        spec.story = story
      } else {
        spec.panels = panels
      }
    } else if (tab === 'image.sequence') {
      spec.shots = shots.filter((s) => s.trim())
    } else if (tab === 'video.single') {
      spec.text = vText
      spec.duration_seconds = duration
      spec.resolution = resolution
      if (refMode === 'none') spec.ratio = ratio
      if (firstFrameAssetId) spec.first_frame_asset_id = firstFrameAssetId
      if (lastFrameAssetId) spec.last_frame_asset_id = lastFrameAssetId
      if (refImageIds.length) spec.reference_image_asset_ids = refImageIds
      if (refVideoIds.length) spec.reference_video_asset_ids = refVideoIds
      if (promptEnhance) spec.prompt_enhance = true
    } else {
      spec.shots = vsShots.map((s) => s.text).filter((t) => t.trim())
      spec.duration_seconds = vsDuration
      spec.ratio = vsRatio
      spec.recalibrate_every = vsRecalibrateEvery
    }
    return spec
  }

  // Instant local guess (pricing.ts mirrors jobsvc.EstimateCredits' formulas
  // exactly) shown until the debounced §13.3 POST /jobs/estimate call
  // resolves and becomes the source of truth — avoids the number sitting at
  // "✦ 0" for the ~300ms+RTT before the first real estimate lands, while
  // still converging on the server's number (the one that actually gets
  // held) rather than a client-side guess that could drift from it if
  // pricing ever changes server-side.
  const localEstimateGuess =
    tab === 'image.single'
      ? estimateImageCredits(1)
      : tab === 'image.batch'
        ? estimateImageCredits(n)
        : tab === 'image.comic4'
          ? estimateImageCredits(4) + (comicMode === 'auto' ? estimateStorySplitCredits() : 0)
          : tab === 'image.sequence'
            ? estimateImageCredits(shots.filter((s) => s.trim()).length || 1)
            : tab === 'video.single'
              ? estimateVideoCredits(duration, resolution) +
                (promptEnhance ? estimatePromptEnhanceCredits() : 0)
              : estimateVideoCredits(vsDuration, '768P') * (vsShots.filter((s) => s.text.trim()).length || 1)

  const debouncedSpecKey = useDebouncedValue(JSON.stringify({ tab, spec: buildSpec() }), 300)
  const estimateQuery = useQuery({
    queryKey: ['estimate', debouncedSpecKey],
    queryFn: () => {
      const parsed = JSON.parse(debouncedSpecKey) as { tab: WorkflowName; spec: Spec }
      return api.estimateJob(parsed.tab, parsed.spec)
    },
    enabled: !isGuest,
    placeholderData: keepPreviousData,
  })
  const estimate = estimateQuery.data?.credits_total ?? localEstimateGuess

  const balance = me.data?.balance ?? 0
  const insufficientBalance = !isGuest && me.isSuccess && balance < estimate

  const createJob = useMutation({
    mutationFn: () => {
      if (tab === 'image.comic4' && comicMode === 'auto' && !story.trim()) {
        throw new Error(t('studio.errors.storyRequired'))
      }
      if (tab === 'video.single' && !videoValidation.success) {
        throw new Error(
          videoValidation.error.issues[0]?.message
            ? t(videoValidation.error.issues[0].message)
            : t('studio.errors.paramsInvalid'),
        )
      }
      if (tab === 'video.sequence' && vsShots.filter((s) => s.text.trim()).length === 0) {
        throw new Error(t('studio.errors.needOneShot'))
      }
      const idemKey = crypto.randomUUID()
      return api.createJob(tab as WorkflowName, buildSpec(), idemKey)
    },
    onSuccess: (res) => {
      setBizId(res.biz_id)
      me.refetch()
    },
    onError: () => pushToast(t('studio.errors.submitFailed'), () => createJob.mutate()),
  })

  const jobStream = useJobStream(bizId)
  const job = jobStream.data as JobResponse | undefined

  // F7.4: mirrors JobDetail's own cancelJob mutation (same endpoint, same
  // "no optimistic update, just refetch" reasoning — see that file's doc).
  const cancelJob = useMutation({
    mutationFn: () => api.cancelJob(bizId!),
    onSuccess: () => jobStream.refetch(),
    onError: () => pushToast(t('jobDetail.cancelFailed'), () => cancelJob.mutate()),
  })

  useEffect(() => {
    if (jobStream.isError) {
      pushToast(t('jobDetail.connectionUnstable'), () => jobStream.refetch())
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jobStream.isError, jobStream.refetch, pushToast])

  const assetIds = resultAssetIds(job, tab)
  const running = !!bizId && job?.status !== 'succeeded' && job?.status !== 'failed'
  const gateNode = job?.nodes.find((n) => n.name === 'gate')
  const gateSuspended = tab === 'video.sequence' && gateNode?.phase === 'Suspended'
  const suggestions = job && job.status === 'succeeded' ? suggestActions(job, tab, assetIds, t) : []

  function applySuggestion(action: SuggestedAction) {
    switch (action.kind) {
      case 'to-video':
        setBizId(null)
        setTab('video.single')
        setRefModeAndClear('firstLast')
        setFirstFrameAssetId(action.sourceAssetId)
        break
      case 'more-batch':
        createJob.mutate()
        break
      case 'save-character':
      case 'save-frame-character':
        navigate('/characters', { state: { prefillAssetId: action.sourceAssetId } })
        break
      case 'to-sequence':
        setBizId(null)
        setTab('video.sequence')
        setVsShots(toShotItems(action.shots))
        break
      case 'upgrade-2k':
        setBizId(null)
        setResolution('2K')
        break
    }
  }

  return (
    <AppShell>
      <div className="mx-auto max-w-4xl space-y-8 px-6 py-10">
        <header className="text-center">
          <h1 className="text-3xl font-semibold tracking-tight">
            <select
              value={tab}
              onChange={(e) => setTab(e.target.value as Tab)}
              className="appearance-none border-b-2 border-dashed border-violet-500/60 bg-transparent px-1 text-violet-400 outline-none"
            >
              {TABS.map((tb) => (
                <option key={tb} value={tb} className="bg-zinc-900 text-zinc-100">
                  {t(WORKFLOW_LABEL_KEY[tb])}
                </option>
              ))}
            </select>{' '}
            {t('studio.headlineSuffix')}
          </h1>
          {isGuest && <p className="mt-2 text-sm text-zinc-500">{t('studio.guestHint')}</p>}
        </header>

        {/* ── Composer ─────────────────────────────────────────── */}
        <div className="space-y-4 rounded-2xl border border-zinc-800 bg-zinc-900/60 p-5">
          {(tab === 'image.single' || tab === 'image.batch') && (
            <textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              rows={3}
              className="w-full resize-none rounded-xl border border-zinc-800 bg-zinc-950 p-4 outline-none focus:border-violet-500"
              placeholder={t('studio.examples.rooftop')}
            />
          )}

          {tab === 'image.single' && (
            <div>
              <p className="mb-2 text-xs uppercase tracking-wide text-zinc-500">{t('studio.image2imageRef')}</p>
              <AssetPicker
                type="image"
                selected={sourceImageId ? [sourceImageId] : []}
                onToggle={(id) => setSourceImageId((cur) => (cur === id ? '' : id))}
                max={1}
              />
            </div>
          )}

          {tab === 'image.comic4' && (
            <div className="space-y-3">
              <div className="flex gap-2 text-sm">
                <button
                  onClick={() => setComicMode('manual')}
                  className={`rounded-lg px-3 py-1.5 ${comicMode === 'manual' ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-400 hover:bg-zinc-950'}`}
                >
                  {t('studio.comic.manual')}
                </button>
                <button
                  onClick={() => setComicMode('auto')}
                  className={`rounded-lg px-3 py-1.5 ${comicMode === 'auto' ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-400 hover:bg-zinc-950'}`}
                  title={t('studio.comic.autoTooltip')}
                >
                  {t('studio.comic.auto')}
                </button>
              </div>

              {comicMode === 'manual' ? (
                <div className="grid grid-cols-2 gap-3">
                  {panels.map((p, i) => (
                    <textarea
                      key={i}
                      value={p}
                      onChange={(e) => setPanels((cur) => cur.map((c, ci) => (ci === i ? e.target.value : c)))}
                      rows={3}
                      className="resize-none rounded-xl border border-zinc-800 bg-zinc-950 p-3 text-sm outline-none focus:border-violet-500"
                      placeholder={t('studio.comic.panelPlaceholder', { n: i + 1 })}
                    />
                  ))}
                </div>
              ) : (
                <textarea
                  value={story}
                  onChange={(e) => setStory(e.target.value)}
                  rows={4}
                  className="w-full resize-none rounded-xl border border-zinc-800 bg-zinc-950 p-4 outline-none focus:border-violet-500"
                  placeholder={t('studio.comic.storyPlaceholder')}
                />
              )}
            </div>
          )}

          {tab === 'image.sequence' && (
            <ShotList
              shots={shots}
              setShots={setShots}
              placeholder={(i) => t('studio.imageSequence.shotPlaceholder', { n: i + 1 })}
              addLabel={t('studio.imageSequence.addLabel')}
            />
          )}

          {tab === 'video.single' && (
            <div className="space-y-4">
              <textarea
                value={vText}
                onChange={(e) => setVText(e.target.value)}
                rows={3}
                className="w-full resize-none rounded-xl border border-zinc-800 bg-zinc-950 p-4 outline-none focus:border-violet-500"
                placeholder={t('studio.examples.foxVideo')}
              />

              <div>
                <Capsule>
                  <span className="text-zinc-500">{t('studio.capsule.refMode')}</span>
                  <select
                    value={refMode}
                    onChange={(e) => setRefModeAndClear(e.target.value as RefMode)}
                    className="bg-transparent text-zinc-100 outline-none"
                  >
                    <option value="none" className="bg-zinc-900">
                      {t('studio.refMode.none')}
                    </option>
                    <option value="firstLast" className="bg-zinc-900">
                      {t('studio.refMode.firstLast')}
                    </option>
                    <option value="reference" className="bg-zinc-900">
                      {t('studio.refMode.reference')}
                    </option>
                  </select>
                </Capsule>

                {refMode === 'firstLast' && (
                  <div className="mt-3 space-y-2">
                    <div>
                      <p className="mb-1 text-xs text-zinc-600">{t('studio.firstFrame')}</p>
                      <AssetPicker
                        type="image"
                        selected={firstFrameAssetId ? [firstFrameAssetId] : []}
                        onToggle={(id) => setFirstFrameAssetId((cur) => (cur === id ? '' : id))}
                        max={1}
                      />
                    </div>
                    <div>
                      <p className="mb-1 text-xs text-zinc-600">{t('studio.lastFrame')}</p>
                      <AssetPicker
                        type="image"
                        selected={lastFrameAssetId ? [lastFrameAssetId] : []}
                        onToggle={(id) => setLastFrameAssetId((cur) => (cur === id ? '' : id))}
                        max={1}
                      />
                    </div>
                  </div>
                )}

                {refMode === 'reference' && (
                  <div className="mt-3 space-y-2">
                    <div>
                      <p className="mb-1 text-xs text-zinc-600">{t('studio.refImages')}</p>
                      <AssetPicker
                        type="image"
                        selected={refImageIds}
                        onToggle={(id) =>
                          setRefImageIds((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]))
                        }
                      />
                    </div>
                    <div>
                      <p className="mb-1 text-xs text-zinc-600">{t('studio.refVideos')}</p>
                      <AssetPicker
                        type="video"
                        selected={refVideoIds}
                        onToggle={(id) =>
                          setRefVideoIds((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]))
                        }
                      />
                    </div>
                  </div>
                )}
              </div>

              <label className="flex items-center gap-2 text-sm text-zinc-400" title={t('studio.promptEnhanceTooltip')}>
                <input
                  type="checkbox"
                  checked={promptEnhance}
                  onChange={(e) => setPromptEnhance(e.target.checked)}
                  className="accent-violet-500"
                />
                {t('studio.promptEnhance', { cost: estimatePromptEnhanceCredits() })}
              </label>
            </div>
          )}

          {tab === 'video.sequence' && (
            <div className="space-y-2">
              <DndContext
                sensors={dndSensors}
                collisionDetection={closestCenter}
                onDragEnd={(e: DragEndEvent) => {
                  const { active, over } = e
                  if (!over || active.id === over.id) return
                  setVsShots((cur) => {
                    const oldIndex = cur.findIndex((s) => s.id === active.id)
                    const newIndex = cur.findIndex((s) => s.id === over.id)
                    return oldIndex === -1 || newIndex === -1 ? cur : arrayMove(cur, oldIndex, newIndex)
                  })
                }}
              >
                <SortableContext items={vsShots.map((s) => s.id)} strategy={verticalListSortingStrategy}>
                  {vsShots.map((shot, i) => (
                    <SortableShotRow
                      key={shot.id}
                      shot={shot}
                      index={i}
                      mode={shotMode(i + 1, vsRecalibrateEvery, !!slotA)}
                      onChange={(text) => setVsShots((cur) => cur.map((s) => (s.id === shot.id ? { ...s, text } : s)))}
                      onRemove={() => setVsShots((cur) => cur.filter((s) => s.id !== shot.id))}
                      removable={vsShots.length > 1}
                    />
                  ))}
                </SortableContext>
              </DndContext>
              <button
                onClick={() => setVsShots((cur) => [...cur, { id: crypto.randomUUID(), text: '' }])}
                className="text-sm text-violet-400 hover:text-violet-300"
              >
                {t('studio.addSegment')}
              </button>
              {vsShots.length > 4 && (
                <p className="text-xs text-amber-500">{t('studio.driftWarning', { n: vsRecalibrateEvery })}</p>
              )}
            </div>
          )}

          {/* ── Capsule parameter row ──────────────────────────── */}
          <div className="flex flex-wrap items-center gap-2 border-t border-zinc-800 pt-4">
            {tab === 'image.batch' && (
              <Capsule>
                <span className="text-zinc-500">{t('studio.capsule.count')}</span>
                <select value={n} onChange={(e) => setN(Number(e.target.value))} className="bg-transparent text-zinc-100 outline-none">
                  {[2, 4, 6, 9].map((v) => (
                    <option key={v} value={v} className="bg-zinc-900">
                      n={v}
                    </option>
                  ))}
                </select>
              </Capsule>
            )}

            {(tab === 'video.single' || tab === 'video.sequence') && (
              <Capsule>
                <span className="text-zinc-500">{t('studio.capsule.duration')}</span>
                <select
                  value={tab === 'video.single' ? duration : vsDuration}
                  onChange={(e) =>
                    tab === 'video.single' ? setDuration(Number(e.target.value)) : setVsDuration(Number(e.target.value))
                  }
                  className="bg-transparent text-zinc-100 outline-none"
                >
                  {[4, 5, 6, 8, 10, 12, 15].map((v) => (
                    <option key={v} value={v} className="bg-zinc-900">
                      {v}s
                    </option>
                  ))}
                </select>
              </Capsule>
            )}

            {tab === 'video.single' && (
              <Capsule>
                <span className="text-zinc-500">{t('studio.capsule.resolution')}</span>
                <select
                  value={resolution}
                  onChange={(e) => setResolution(e.target.value as '768P' | '2K')}
                  className="bg-transparent text-zinc-100 outline-none"
                >
                  <option value="768P" className="bg-zinc-900">768P</option>
                  <option value="2K" className="bg-zinc-900">2K</option>
                </select>
              </Capsule>
            )}

            {tab === 'video.single' && (
              <Capsule disabled={refMode !== 'none'} title={refMode !== 'none' ? t('studio.capsule.ratioDisabledTooltip') : undefined}>
                <span className="text-zinc-500">{t('studio.capsule.ratio')}</span>
                <select
                  value={ratio}
                  onChange={(e) => setRatio(e.target.value as (typeof RATIO_VALUES)[number])}
                  disabled={refMode !== 'none'}
                  className="bg-transparent text-zinc-100 outline-none disabled:cursor-not-allowed"
                >
                  {RATIO_VALUES.map((r) => (
                    <option key={r} value={r} className="bg-zinc-900">
                      {r}
                    </option>
                  ))}
                </select>
              </Capsule>
            )}

            {tab === 'video.sequence' && (
              <>
                <Capsule>
                  <span className="text-zinc-500">{t('studio.capsule.ratio')}</span>
                  <select value={vsRatio} onChange={(e) => setVsRatio(e.target.value as (typeof RATIO_VALUES)[number])} className="bg-transparent text-zinc-100 outline-none">
                    {RATIO_VALUES.map((r) => (
                      <option key={r} value={r} className="bg-zinc-900">
                        {r}
                      </option>
                    ))}
                  </select>
                </Capsule>
                <Capsule title={t('studio.capsule.anchorIntervalTooltip')}>
                  <span className="text-zinc-500">{t('studio.capsule.anchorInterval')}</span>
                  <select
                    value={vsRecalibrateEvery}
                    onChange={(e) => setVsRecalibrateEvery(Number(e.target.value))}
                    className="bg-transparent text-zinc-100 outline-none"
                  >
                    {[2, 3, 4, 5].map((v) => (
                      <option key={v} value={v} className="bg-zinc-900">
                        {t('studio.capsule.everyNSegments', { n: v })}
                      </option>
                    ))}
                  </select>
                </Capsule>
              </>
            )}

            {!isGuest && (
              <>
                <Capsule>
                  <span className="text-zinc-500">{t('studio.capsule.characterA')}</span>
                  <CharacterSelectInline value={slotA} onChange={setSlotA} options={characters.data?.characters ?? []} />
                </Capsule>
                <Capsule>
                  <span className="text-zinc-500">{t('studio.capsule.characterB')}</span>
                  <CharacterSelectInline value={slotB} onChange={setSlotB} options={characters.data?.characters ?? []} />
                </Capsule>
              </>
            )}

            <div className="flex-1" />

            {!isGuest ? (
              <>
                <div className="text-right font-mono text-sm text-zinc-300">
                  <span className="text-violet-400">✦</span> <AnimatedNumber value={estimate} />
                </div>
                <button
                  onClick={() => createJob.mutate()}
                  disabled={
                    createJob.isPending || running || insufficientBalance || (tab === 'video.single' && !videoValidation.success)
                  }
                  className="rounded-full bg-violet-500 px-5 py-2 text-sm font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
                >
                  {createJob.isPending ? t('studio.submitting') : running ? t('studio.generating') : t('studio.generate')}
                </button>
              </>
            ) : (
              <Link to="/login" className="rounded-full bg-violet-500 px-5 py-2 text-sm font-medium text-white transition hover:bg-violet-400">
                {t('studio.loginToGenerate')}
              </Link>
            )}
          </div>

          {insufficientBalance && <p className="text-sm text-red-400">{t('studio.insufficientBalance', { balance })}</p>}
          {tab === 'video.single' && !videoValidation.success && (
            <p className="text-sm text-amber-400">
              {videoValidation.error.issues[0]?.message ? t(videoValidation.error.issues[0].message) : ''}
            </p>
          )}

          {!isGuest && characters.isSuccess && characters.data.characters.length === 0 && (
            <p className="text-xs text-zinc-600">{t('studio.noCharactersHint')}</p>
          )}

          {!isGuest && !!presets.data?.presets.length && (
            <PresetCarousel
              presets={presets.data.presets}
              selected={presetIds}
              onToggle={(id) => setPresetIds((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]))}
            />
          )}
        </div>

        {/* ── Format quick-switch cards ──────────────────────────── */}
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
          {TABS.map((tb) => (
            <button
              key={tb}
              onClick={() => setTab(tb)}
              className={`rounded-xl border p-3 text-left transition ${
                tab === tb ? 'border-violet-500 bg-violet-500/10' : 'border-zinc-800 bg-zinc-900/40 hover:border-zinc-700'
              }`}
            >
              <p className="text-lg leading-none">{TAB_META_KEY[tb].icon}</p>
              <p className={`mt-1.5 text-sm font-medium ${tab === tb ? 'text-violet-300' : 'text-zinc-200'}`}>
                {t(WORKFLOW_LABEL_KEY[tb])}
              </p>
              <p className="mt-0.5 text-xs text-zinc-500">{t(TAB_META_KEY[tb].blurbKey)}</p>
            </button>
          ))}
        </div>

        {bizId && (
          <Link to={`/jobs/${bizId}`} className="text-sm text-violet-400 hover:text-violet-300">
            {t('studio.viewGraph')}
          </Link>
        )}

        {/* ── Results ─────────────────────────────────────────── */}
        <div className="min-h-80 rounded-2xl border border-zinc-800 bg-zinc-900/40 p-6">
          {!bizId && !isGuest && (
            <EmptyState presets={presets.data?.presets ?? []} onPick={(fragment) => (tab === 'video.single' ? setVText(fragment) : setText(fragment))} />
          )}

          {!bizId && isGuest && (
            <div className="mx-auto max-w-md space-y-3 text-center">
              <p className="text-sm text-zinc-400">{t('studio.trial.intro')}</p>
              <textarea
                value={trialPrompt}
                onChange={(e) => setTrialPrompt(e.target.value)}
                rows={2}
                className="w-full resize-none rounded-lg border border-zinc-800 bg-zinc-950 p-2 text-sm outline-none focus:border-violet-500"
              />
              <button
                onClick={runTrial}
                disabled={trialBusy || !trialPrompt.trim()}
                className="w-full rounded-lg border border-zinc-700 px-3 py-2 text-sm text-zinc-200 transition hover:border-zinc-600 hover:bg-zinc-800 disabled:opacity-50"
              >
                {trialBusy ? t('studio.generating') : t('studio.trial.tryOnce')}
              </button>
              {trialError && <p className="text-sm text-red-400">{trialError}</p>}
              {trialImageUrl && (
                <div className="pt-2">
                  <img src={trialImageUrl} alt="" className="mx-auto rounded-lg" />
                  <p className="mt-2 text-xs text-zinc-500">{t('studio.trial.likeIt')}</p>
                </div>
              )}
            </div>
          )}

          {gateSuspended && bizId && job && (
            <PreviewGate bizId={bizId} job={job} duration={vsDuration} onResumed={() => jobStream.refetch()} />
          )}

          {running && !gateSuspended && (
            <div className="flex min-h-64 flex-col items-center justify-center gap-3">
              <GenerationProgress kind={tab.startsWith('video') ? 'video' : 'image'} />
              {jobStream.streamState === 'reconnecting' && (
                <p className="text-xs text-amber-500">{t('jobDetail.reconnecting')}</p>
              )}
              <button
                onClick={() => cancelJob.mutate()}
                disabled={cancelJob.isPending}
                className="rounded-lg border border-zinc-700 px-3 py-1.5 text-xs text-zinc-400 transition hover:border-red-500 hover:text-red-400 disabled:opacity-50"
              >
                {cancelJob.isPending ? t('jobDetail.cancelling') : t('jobDetail.cancelJob')}
              </button>
            </div>
          )}

          {job?.status === 'failed' && (
            <p className="text-center text-red-400">
              {t('studio.generationFailed', { error: displayNodeError(job && firstSpecificError(job.nodes), t) })}
            </p>
          )}

          {job?.status === 'succeeded' && (
            <div className="space-y-4">
              <div className="flex flex-wrap items-center justify-center gap-3">
                {assetIds.map((id) => (
                  <div key={id} className="text-center">
                    <p className="mb-1 font-mono text-xs text-zinc-500">{id}</p>
                    <GeneratedMedia assetId={id} />
                  </div>
                ))}
              </div>

              {suggestions.length > 0 && (
                <div className="flex flex-wrap items-center justify-center gap-2 border-t border-zinc-800 pt-4">
                  <span className="text-xs text-zinc-500">{t('studio.suggestionsLabel')}</span>
                  {suggestions.map((s) => (
                    <button
                      key={s.kind}
                      onClick={() => applySuggestion(s)}
                      className="rounded-full border border-zinc-700 bg-zinc-950 px-3 py-1.5 text-xs text-zinc-200 transition hover:border-violet-500 hover:text-violet-300"
                    >
                      {s.icon} {s.label}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </AppShell>
  )
}

function Capsule({ children, disabled, title }: { children: ReactNode; disabled?: boolean; title?: string }) {
  return (
    <div
      title={title}
      className={`flex items-center gap-1.5 rounded-full border border-zinc-800 bg-zinc-950 px-3 py-1.5 text-xs text-zinc-300 ${disabled ? 'opacity-40' : ''}`}
    >
      {children}
    </div>
  )
}

// §19.4.4's drag-reorder shot card. useSortable needs its own component
// (a hook, so it can't be called inline inside vsShots.map's callback) —
// mode is passed in already-computed since it depends on this row's
// position, which SortableShotRow itself has no reason to know about.
function SortableShotRow({
  shot,
  index,
  mode,
  onChange,
  onRemove,
  removable,
}: {
  shot: ShotItem
  index: number
  mode: ShotMode
  onChange: (text: string) => void
  onRemove: () => void
  removable: boolean
}) {
  const { t } = useTranslation()
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: shot.id })
  const style = { transform: CSS.Transform.toString(transform), transition, opacity: isDragging ? 0.5 : 1 }

  return (
    <div ref={setNodeRef} style={style} className="flex items-center gap-2">
      <button
        type="button"
        {...attributes}
        {...listeners}
        title={t('studio.dragToReorder')}
        className="shrink-0 cursor-grab touch-none px-1 text-zinc-600 transition hover:text-zinc-400 active:cursor-grabbing"
      >
        ⋮⋮
      </button>
      <span
        title={t(SHOT_MODE_LABEL_KEY[mode])}
        className={`shrink-0 rounded-full border px-2 py-1 text-[11px] font-mono ${SHOT_MODE_CLASS[mode]}`}
      >
        {mode}
      </span>
      <input
        value={shot.text}
        onChange={(e) => onChange(e.target.value)}
        className="flex-1 rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
        placeholder={t('studio.videoSequence.shotPlaceholder', { n: index + 1 })}
      />
      {removable && (
        <button
          onClick={onRemove}
          className="shrink-0 rounded-lg border border-zinc-800 px-2 text-zinc-500 hover:text-red-400"
        >
          ×
        </button>
      )}
    </div>
  )
}

function ShotList({
  shots,
  setShots,
  placeholder,
  addLabel,
}: {
  shots: string[]
  setShots: React.Dispatch<React.SetStateAction<string[]>>
  placeholder: (i: number) => string
  addLabel: string
}) {
  return (
    <div className="space-y-2">
      {shots.map((s, i) => (
        <div key={i} className="flex gap-2">
          <input
            value={s}
            onChange={(e) => setShots((cur) => cur.map((c, ci) => (ci === i ? e.target.value : c)))}
            className="flex-1 rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
            placeholder={placeholder(i)}
          />
          {shots.length > 1 && (
            <button
              onClick={() => setShots((cur) => cur.filter((_, ci) => ci !== i))}
              className="rounded-lg border border-zinc-800 px-2 text-zinc-500 hover:text-red-400"
            >
              ×
            </button>
          )}
        </div>
      ))}
      <button onClick={() => setShots((cur) => [...cur, ''])} className="text-sm text-violet-400 hover:text-violet-300">
        {addLabel}
      </button>
    </div>
  )
}

// F19.4.1's cold-start empty state: reuses preset cover images (already
// fetched for the carousel above) instead of a bare placeholder — clicking
// one drops its prompt fragment straight into the active input.
function EmptyState({ presets, onPick }: { presets: { biz_id: string; name: string; cover_url: string; prompt_fragment: string }[]; onPick: (fragment: string) => void }) {
  const { t } = useTranslation()
  const sample = presets.slice(0, 4)
  if (sample.length === 0) {
    return <p className="text-center text-zinc-500">{t('studio.emptyResult')}</p>
  }
  return (
    <div className="mx-auto max-w-md text-center">
      <p className="mb-3 text-sm text-zinc-500">{t('studio.guessWhat')}</p>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        {sample.map((p) => (
          <button
            key={p.biz_id}
            onClick={() => onPick(p.prompt_fragment)}
            className="group overflow-hidden rounded-xl border border-zinc-800 transition hover:border-violet-500"
          >
            {p.cover_url ? (
              <img src={p.cover_url} alt="" className="aspect-square w-full object-cover" />
            ) : (
              <div className="aspect-square w-full bg-zinc-800" />
            )}
            <p className="truncate bg-zinc-950 px-2 py-1 text-xs text-zinc-400 group-hover:text-violet-300">{p.name}</p>
          </button>
        ))}
      </div>
    </div>
  )
}

function CharacterSelectInline({
  value,
  onChange,
  options,
}: {
  value: string
  onChange: (v: string) => void
  options: { biz_id: string; name: string }[]
}) {
  const { t } = useTranslation()
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)} className="bg-transparent text-zinc-100 outline-none">
      <option value="" className="bg-zinc-900">
        {t('studio.unselected')}
      </option>
      {options.map((c) => (
        <option key={c.biz_id} value={c.biz_id} className="bg-zinc-900">
          {c.name}
        </option>
      ))}
    </select>
  )
}

// F2.4/F2.5 asset detail (full version — filters, "以此再生成" — is a later
// pass); this is the minimal lookup so Studio renders what the executor
// actually materialized, not a client-side stand-in. video.single's result
// is a video asset, so this renders <video> or <img> based on what came back.
function GeneratedMedia({ assetId }: { assetId: string }) {
  const { data } = useQuery({
    queryKey: ['asset', assetId],
    queryFn: () => api.getAsset(assetId),
  })
  if (!data) {
    return <div className="h-64 w-64 animate-pulse rounded-lg bg-zinc-800" />
  }
  if (data.type === 'video') {
    return <video src={data.public_url} controls className="h-64 w-64 rounded-lg bg-black object-contain" />
  }
  return <img src={data.public_url} alt="generated" className="h-64 w-64 rounded-lg object-cover" />
}
