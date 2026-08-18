import { useEffect, useRef, useState, type ReactNode } from 'react'
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
import { MentionTextarea } from '../components/MentionTextarea'
import { CharacterSlotPicker } from '../components/CharacterSlotPicker'
import HomeFeed from '../components/HomeFeed'
import { useAuthStore } from '../lib/authStore'
import { getDeviceId } from '../lib/deviceId'
import { useJobStream } from '../hooks/useJobStream'
import { useDebouncedValue } from '../hooks/useDebouncedValue'
import { estimateWaitSeconds, formatWaitMinutes } from '../lib/durationEstimate'

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

const TAB_META_KEY: Record<Tab, { blurbKey: string }> = {
  'image.single': { blurbKey: 'studio.tabMeta.imageSingle' },
  'image.batch': { blurbKey: 'studio.tabMeta.imageBatch' },
  'image.comic4': { blurbKey: 'studio.tabMeta.imageComic4' },
  'image.sequence': { blurbKey: 'studio.tabMeta.imageSequence' },
  'video.single': { blurbKey: 'studio.tabMeta.videoSingle' },
  'video.sequence': { blurbKey: 'studio.tabMeta.videoSequence' },
}
const TABS = Object.keys(TAB_META_KEY) as Tab[]
const SLOT_LETTERS = 'ABCDEF'

// Line-icon set for the format cards (§07's "不要用emoji" gap — same
// reasoning as NotificationCenter's bell: emoji render inconsistently
// across platforms and read as a placeholder, not a considered icon).
// Plain stroke shapes, no fill, matching that bell icon's own style.
function TabIcon({ tab, className }: { tab: Tab; className?: string }) {
  const common = { viewBox: '0 0 20 20', fill: 'none', stroke: 'currentColor', strokeWidth: 1.6, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const, className }
  switch (tab) {
    case 'image.single':
      return (
        <svg {...common}>
          <rect x="2.5" y="3.5" width="15" height="13" rx="2" />
          <circle cx="7" cy="8" r="1.3" />
          <path d="M3.5 14.5l4-4 3 3 3.5-4.5 4.5 5.5" />
        </svg>
      )
    case 'image.batch':
      return (
        <svg {...common}>
          <rect x="6.5" y="2.5" width="11" height="11" rx="1.8" />
          <rect x="2.5" y="6.5" width="11" height="11" rx="1.8" />
        </svg>
      )
    case 'image.comic4':
      return (
        <svg {...common}>
          <rect x="2.5" y="2.5" width="15" height="15" rx="1.5" />
          <path d="M10 2.5v15M2.5 10h15" />
        </svg>
      )
    case 'image.sequence':
      return (
        <svg {...common}>
          <rect x="1.75" y="8.25" width="3.5" height="3.5" rx="0.8" />
          <rect x="8.25" y="8.25" width="3.5" height="3.5" rx="0.8" />
          <rect x="14.75" y="8.25" width="3.5" height="3.5" rx="0.8" />
          <path d="M5.25 10h3M11.75 10h3" />
        </svg>
      )
    case 'video.single':
      return (
        <svg {...common}>
          <rect x="2.5" y="3.5" width="15" height="13" rx="2" />
          <path d="M8 7.3l5 2.7-5 2.7z" />
        </svg>
      )
    case 'video.sequence':
      return (
        <svg {...common}>
          <rect x="2.5" y="3" width="15" height="14" rx="1.5" />
          <path d="M2.5 6.7h15M2.5 13.3h15" />
          <path d="M8 8.7l4 1.8-4 1.8z" />
        </svg>
      )
  }
}

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
  return useQuery({ queryKey: ['characters'], queryFn: () => api.listCharacters(), enabled })
}
function usePresets(enabled: boolean) {
  return useQuery({ queryKey: ['presets'], queryFn: () => api.listPresets(), enabled })
}
function useMe(enabled: boolean) {
  return useQuery({ queryKey: ['me'], queryFn: api.me, enabled })
}
function useProjects(enabled: boolean) {
  return useQuery({ queryKey: ['projects'], queryFn: api.listProjects, enabled })
}

// Static fallbacks, used only until GET /capabilities resolves (or if it
// ever fails) — kept identical to the backend's own capability package so
// there's no visible flash of different options, just a source-of-truth
// swap once the fetch lands. See lib/api.ts's getCapabilities doc.
const FALLBACK_BATCH_N = [2, 4, 6, 9]
const FALLBACK_DURATIONS = [4, 5, 6, 8, 10, 12, 15]
const FALLBACK_RESOLUTIONS = ['768P', '2K']

export default function Studio() {
  const { t } = useTranslation()
  const accessToken = useAuthStore((s) => s.accessToken)
  const isGuest = !accessToken
  const navigate = useNavigate()
  const location = useLocation()

  const capabilities = useQuery({ queryKey: ['capabilities'], queryFn: api.getCapabilities, staleTime: Infinity })
  const batchNOptions = capabilities.data
    ? FALLBACK_BATCH_N.filter((v) => v <= capabilities.data.image.max_n)
    : FALLBACK_BATCH_N
  const durationOptions = capabilities.data
    ? FALLBACK_DURATIONS.filter((v) => v >= capabilities.data.video.duration_min && v <= capabilities.data.video.duration_max)
    : FALLBACK_DURATIONS
  const resolutionOptions = capabilities.data?.video.resolutions ?? FALLBACK_RESOLUTIONS
  const ratioOptions = capabilities.data?.video.ratios ?? RATIO_VALUES

  const [tab, setTab] = useState<Tab>('image.single')
  // Starts empty, not pre-filled with the example — a filled composer
  // meant deleting placeholder text before typing your own prompt, every
  // time (§07 gap: found live during review). studio.examples.rooftop
  // already covers the same job as a real placeholder below.
  const [text, setText] = useState('')
  const [n, setN] = useState(4)
  const [panels, setPanels] = useState(['', '', '', ''])
  const [shots, setShots] = useState([''])
  // image.sequence's cross-shot referencing (Spec.ShotSourceRefs' own doc):
  // parallel array to shots, null/0 = no reference, else the 1-based index
  // of an earlier shot in shots whose generated image becomes this shot's
  // own source-image-asset-id. Kept as real state (not parsed back out of
  // the mention token text) — same "state is the source of truth, the
  // inserted text is cosmetic" pattern sourceImageId/refImageIds etc.
  // already use everywhere else in this file.
  const [shotSourceRefs, setShotSourceRefs] = useState<(number | null)[]>([null])
  // §07's "只能綁定 2 個角色" gap — the backend never actually capped this
  // (CharacterSlot's own doc: "slots beyond A/B are accepted but the PRD
  // only defines those two", and prompt.Compile embeds every bound
  // character's description with no length of its own), so the fixed
  // slotA/slotB pair was purely a frontend limitation. A hard cap still
  // makes sense though: each extra character only ever contributes a text
  // description here (no visual reference — MiniMax's subject_reference
  // is source-image-asset-id's separate img2img mechanism, F5.8, not
  // wired to character slots at all), and they all compete for the same
  // 1500-char image prompt budget, so an unbounded list would just start
  // silently losing characters to the compiler's own trim step.
  const MAX_CHARACTER_SLOTS = 6
  const [characterSlotIds, setCharacterSlotIds] = useState<string[]>(['', ''])
  const [presetIds, setPresetIds] = useState<string[]>([])
  const [bizId, setBizId] = useState<string | null>(null)
  const [projectId, setProjectId] = useState('')
  const [styleFilter, setStyleFilter] = useState('')
  const [savingPreset, setSavingPreset] = useState(false)
  const [newPresetName, setNewPresetName] = useState('')
  const [showBreakdown, setShowBreakdown] = useState(false)

  // video.single-only state (F6.1-F6.5).
  const [vText, setVText] = useState('')
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
  // comic4's 快速/連貫模式 toggle (jobsvc.go's Spec.Comic4Mode doc) —
  // independent of comicMode above (manual/auto is about *where the text
  // comes from*, quick/continuity is about *how consistent the 4 images
  // are*, the two axes are orthogonal).
  const [comic4Mode, setComic4Mode] = useState<'quick' | 'continuity'>('quick')
  // image.sequence's own equivalent toggle (Spec.ImageSequenceMode doc).
  const [imageSequenceMode, setImageSequenceMode] = useState<'quick' | 'continuity'>('quick')

  // video.sequence-only state (F6.7/F6.8).
  const [vsShots, setVsShots] = useState<ShotItem[]>(() => toShotItems(['']))
  const [vsDuration, setVsDuration] = useState(5)
  const [vsRatio, setVsRatio] = useState<(typeof RATIO_VALUES)[number]>('16:9')
  const [vsRecalibrateEvery, setVsRecalibrateEvery] = useState(3)
  const [vsSkipPreview, setVsSkipPreview] = useState(false)
  // Narrative-continuity feature (Spec.NarrativeContinuity doc): off by
  // default, matches every existing job's behavior exactly. refMode picks
  // how an anchor's bundle gets built once narrativeContinuity is on —
  // mirrors video.single's own refMode selector's binary-choice UX
  // (Seedance's own pattern the user pointed at), just three options
  // instead of two.
  const [narrativeContinuity, setNarrativeContinuity] = useState(false)
  const [vsReferenceSelectionMode, setVsReferenceSelectionMode] = useState<'window' | 'manual' | 'smart'>('window')
  // video.sequence's own #-mention override (Spec.ShotReferenceOverrides
  // doc) — same shape and same UI pattern as image.sequence's
  // shotSourceRefs, kept as its own state since the two workflows' shots
  // arrays are otherwise unrelated.
  const [vsShotReferenceOverrides, setVsShotReferenceOverrides] = useState<(number | null)[]>([null])
  // video.sequence's r2va anchor when it's a video rather than an image
  // (Spec.SourceVideoAssetID's own doc) — mutually exclusive with
  // sourceImageId, which video.sequence's own anchor block below also uses.
  const [sourceVideoId, setSourceVideoId] = useState('')

  // F2.5's "以此再生成": AssetDetail navigates here with the source job's
  // exact workflow_name/spec in router state — buildSpec()'s inverse,
  // mapping that Spec back onto every piece of local state it came from.
  // Guarded to run once per navigation (not on every render): it's meant
  // to seed the form, not keep clobbering whatever the user types next.
  // Characters.tsx's "用這個角色創作" (§07's quick-create-shortcut gap, the
  // reverse direction of it): a much lighter prefill than prefillJob below
  // — just drop the character into slot A, no workflow/spec to restore.
  useEffect(() => {
    const prefillCharacterId = (location.state as { prefillCharacterId?: string } | null)?.prefillCharacterId
    if (!prefillCharacterId) return
    setCharacterSlotIds((cur) => [prefillCharacterId, ...cur.slice(1)])
    navigate('.', { replace: true, state: {} })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state])

  useEffect(() => {
    const prefill = (location.state as { prefillJob?: { workflowName: Tab; spec: Spec } } | null)?.prefillJob
    if (!prefill) return
    const { workflowName, spec } = prefill
    setTab(workflowName)
    setBizId(null)

    const boundIds = (spec.characters ?? []).map((c) => c.character_id)
    setCharacterSlotIds(boundIds.length ? boundIds : ['', ''])
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
      setComic4Mode(spec.comic4_mode === 'continuity' ? 'continuity' : 'quick')
    } else if (workflowName === 'image.sequence') {
      const restoredShots = spec.shots?.length ? spec.shots : ['']
      setShots(restoredShots)
      const restoredRefs = spec.shot_source_refs ?? []
      setShotSourceRefs(restoredShots.map((_, i) => restoredRefs[i] || null))
      setSourceImageId(spec.source_image_asset_id ?? '')
      setImageSequenceMode(spec.image_sequence_mode === 'continuity' ? 'continuity' : 'quick')
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
      const restoredVsShots = spec.shots?.length ? spec.shots : ['']
      setVsShots(toShotItems(restoredVsShots))
      if (spec.duration_seconds) setVsDuration(spec.duration_seconds)
      if (spec.ratio) setVsRatio(spec.ratio as (typeof RATIO_VALUES)[number])
      if (spec.recalibrate_every) setVsRecalibrateEvery(spec.recalibrate_every)
      setVsSkipPreview(!!spec.skip_preview)
      setSourceImageId(spec.source_image_asset_id ?? '')
      setSourceVideoId(spec.source_video_asset_id ?? '')
      setNarrativeContinuity(!!spec.narrative_continuity)
      setVsReferenceSelectionMode(spec.reference_selection_mode ?? 'window')
      const restoredVsRefs = spec.shot_reference_overrides ?? []
      setVsShotReferenceOverrides(restoredVsShots.map((_, i) => restoredVsRefs[i] || null))
    }

    // Clear the router state so refreshing or navigating back here later
    // doesn't silently re-apply a stale prefill over new edits.
    navigate('.', { replace: true, state: {} })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state])

  const me = useMe(!isGuest)
  const characters = useCharacters(!isGuest)
  const presets = usePresets(!isGuest)
  const projects = useProjects(!isGuest)
  const pushToast = useToast()

  // F4.5's "另存為我的預設": saves whatever the active tab's own free-text
  // field currently holds as prompt_fragment — there's no single "the
  // prompt" field across all six tabs, so this picks the one the current
  // tab actually uses (falls back to '' for tabs with no single text field,
  // which the disabled save button below already prevents from being hit).
  const savePreset = useMutation({
    mutationFn: () => {
      const fragment = tab === 'video.single' ? vText : tab === 'image.comic4' ? (comicMode === 'auto' ? story : panels[0]) : text
      return api.createPreset({ name: newPresetName, prompt_fragment: fragment, style_type: styleFilter || undefined })
    },
    onSuccess: () => {
      presets.refetch()
      setSavingPreset(false)
      setNewPresetName('')
    },
    onError: () => pushToast(t('studio.presetSaveFailed'), () => savePreset.mutate()),
  })

  // A code-review pass caught that guarding re-entrancy off
  // deletePreset.isPending doesn't actually work: two clicks fired in the
  // same synchronous burst (a real double-click, or rapid taps) both read
  // isPending from the same stale render closure — React hasn't re-rendered
  // between them yet, so both see false and both call .mutate(). Verified
  // live: three synchronous clicks produced three real DELETE requests
  // despite that guard. A ref mutates in place and is read synchronously
  // within the same call, so it can't go stale between two clicks the way
  // component state can — deletingPresetId is kept alongside purely to
  // drive the button's visual disabled state, not for the guard itself.
  const deletingPresetIds = useRef(new Set<string>())
  const [deletingPresetId, setDeletingPresetId] = useState<string | undefined>()
  const deletePreset = useMutation({
    mutationFn: (bizId: string) => api.deletePreset(bizId),
    onSuccess: (_data, bizId) => {
      setPresetIds((cur) => cur.filter((id) => id !== bizId))
      presets.refetch()
    },
    onError: () => pushToast(t('studio.presetDeleteFailed')),
    onSettled: (_data, _error, bizId) => {
      deletingPresetIds.current.delete(bizId)
      setDeletingPresetId((cur) => (cur === bizId ? undefined : cur))
    },
  })
  function handleDeletePreset(bizId: string) {
    if (deletingPresetIds.current.has(bizId)) return
    deletingPresetIds.current.add(bizId)
    setDeletingPresetId(bizId)
    deletePreset.mutate(bizId)
  }
  // §19.4.4's shot drag-reorder. A small activation distance keeps a plain
  // click on the drag handle from being misread as a drag when the pointer
  // moves a pixel or two before release.
  const dndSensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  // F1.2's anonymous trial — relocated here from the login page ("give
  // value before the wall" only works if the value is visible before the
  // wall, not after it). Self-contained, doesn't touch jobs/credits/assets.
  // Shares the main composer's own `text` field rather than a second prompt
  // input — a guest used to have to retype their prompt into a duplicate
  // box further down the page just to actually generate anything, which is
  // exactly the box this replaces (§07 gap: found live during review).
  const [trialImageUrl, setTrialImageUrl] = useState<string | null>(null)
  const [trialError, setTrialError] = useState<string | null>(null)
  const [trialBusy, setTrialBusy] = useState(false)

  async function runTrial() {
    setTrialBusy(true)
    setTrialError(null)
    try {
      const res = await api.trialImage(text, getDeviceId())
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
    const characterSlots = characterSlotIds
      .map((id, i) => id && { slot: SLOT_LETTERS[i], character_id: id })
      .filter(Boolean) as Spec['characters']

    const spec: Spec = {
      characters: characterSlots?.length ? characterSlots : undefined,
      preset_ids: presetIds.length ? presetIds : undefined,
    }
    if (tab === 'image.single' || tab === 'image.batch') {
      spec.text = text
      if (tab === 'image.batch') spec.n = n
      if (sourceImageId) spec.source_image_asset_id = sourceImageId
    } else if (tab === 'image.comic4') {
      if (comicMode === 'auto') {
        spec.story = story
      } else {
        spec.panels = panels
      }
      if (sourceImageId) spec.source_image_asset_id = sourceImageId
      if (comic4Mode === 'continuity') spec.comic4_mode = 'continuity'
    } else if (tab === 'image.sequence') {
      // Blank shots get dropped, which shifts every later shot's position —
      // filterShotsWithRefs renumbers shotSourceRefs' 1-based indices (and
      // drops/orphans a ref whose target itself got dropped) so a blank row
      // left in the middle can never silently point cross-shot refs at the
      // wrong shot.
      const { shots: filteredShots, refs } = filterShotsWithRefs(shots, shotSourceRefs)
      spec.shots = filteredShots
      if (refs.some((r) => r > 0)) spec.shot_source_refs = refs
      if (sourceImageId) spec.source_image_asset_id = sourceImageId
      if (imageSequenceMode === 'continuity') spec.image_sequence_mode = 'continuity'
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
      // Same blank-shot renumbering concern as image.sequence above —
      // filterShotsWithRefs is generic over any (string[], (number|null)[])
      // pair, not image.sequence-specific, so it's reused unchanged here.
      const { shots: filteredVsShots, refs: vsRefs } = filterShotsWithRefs(
        vsShots.map((s) => s.text),
        vsShotReferenceOverrides,
      )
      spec.shots = filteredVsShots
      spec.duration_seconds = vsDuration
      spec.ratio = vsRatio
      spec.recalibrate_every = vsRecalibrateEvery
      if (vsSkipPreview) spec.skip_preview = true
      if (sourceImageId) spec.source_image_asset_id = sourceImageId
      else if (sourceVideoId) spec.source_video_asset_id = sourceVideoId
      if (narrativeContinuity) {
        spec.narrative_continuity = true
        spec.reference_selection_mode = vsReferenceSelectionMode
      }
      if (vsRefs.some((r) => r > 0)) spec.shot_reference_overrides = vsRefs
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
              : estimateVideoCredits(vsDuration, vsSkipPreview ? '2K' : '768P') * (vsShots.filter((s) => s.text.trim()).length || 1)

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
  const estimateItems = estimateQuery.data?.items ?? []
  // §04's "提交鍵旁的劃線原價" — the one natural, non-fabricated
  // original/discounted pair in this Spec is video.single's 768P vs. 2K
  // cost: at 768P, showing what the same clip would cost at 2K is a real
  // number (creditsvc.EstimateVideoCredits with the same duration, just a
  // different resolution rate), not an invented "was" price. Every other
  // tab has no equivalent natural comparison, so it's left alone rather
  // than manufacturing one.
  const upgradeReferencePrice =
    tab === 'video.single' && resolution === '768P' ? estimateVideoCredits(duration, '2K') : null

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
      return api.createJob(tab as WorkflowName, buildSpec(), idemKey, projectId || undefined)
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
          {/* A native <select> used to live here — its closed state could be
              themed, but the open dropdown list is rendered by the OS/browser
              and ignores nearly all CSS (the oversized, off-palette popup a
              review caught). Since the format cards right below already are
              a fully custom-styled way to switch tabs, this became a plain
              label instead of rebuilding the same picker twice. */}
          <h1 className="text-3xl font-semibold tracking-tight">
            <span className="border-b-2 border-dashed border-violet-500/60 px-1 text-violet-400">
              {t(WORKFLOW_LABEL_KEY[tab])}
            </span>{' '}
            {t('studio.headlineSuffix')}
          </h1>
          {isGuest && <p className="mt-2 text-sm text-zinc-500">{t('studio.guestHint')}</p>}
        </header>

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
              <TabIcon tab={tb} className={`h-5 w-5 ${tab === tb ? 'text-violet-400' : 'text-zinc-500'}`} />
              <p className={`mt-1.5 text-sm font-medium ${tab === tb ? 'text-violet-300' : 'text-zinc-200'}`}>
                {t(WORKFLOW_LABEL_KEY[tb])}
              </p>
              <p className="mt-0.5 text-xs text-zinc-500">{t(TAB_META_KEY[tb].blurbKey)}</p>
            </button>
          ))}
        </div>

        {/* ── Composer ─────────────────────────────────────────── */}
        <div className="space-y-4 rounded-2xl border border-zinc-800 bg-zinc-900/60 p-5">
          {(tab === 'image.single' || tab === 'image.batch') && (
            <div>
              <MentionTextarea
                value={text}
                onChange={setText}
                rows={3}
                placeholder={t('studio.examples.rooftop')}
                onMentionAsset={(asset) => {
                  // #-mentioning an image here also sets it as the shared
                  // source_image_asset_id reference (F5.8, now every
                  // image.* mode's own doc) — matches the visible
                  // AssetPicker right below, same field either way.
                  if (asset.type === 'image') setSourceImageId(asset.biz_id)
                }}
              />
              <CharCount value={text} max={capabilities.data?.image.max_prompt_chars} />
            </div>
          )}

          {/* §07 gap: this used to be image.single only (F5.8's original
              scope) — a user asked why every other mode had no way to
              anchor generation on a reference image at all. jobsvc.go's
              Spec.SourceImageAssetID doc covers why extending it was
              low-risk: minimax.image's subject_reference (and
              video.sequence's r2va anchor) are already per-call, not tied
              to any one workflow shape. */}
          {tab !== 'video.single' && tab !== 'video.sequence' && (
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

              {/* Orthogonal to manual/auto above: that picks where the text
                  comes from, this picks how consistent the 4 resulting
                  images look (jobsvc.go's Spec.Comic4Mode doc). */}
              <div className="flex gap-2 text-sm">
                {(['quick', 'continuity'] as const).map((m) => (
                  <button
                    key={m}
                    onClick={() => setComic4Mode(m)}
                    title={t(`studio.consistencyMode.${m}Tooltip`)}
                    className={`rounded-lg border px-3 py-1.5 ${
                      comic4Mode === m ? 'border-violet-500 bg-violet-500/10 text-violet-300' : 'border-zinc-800 text-zinc-400 hover:border-zinc-700'
                    }`}
                  >
                    {t(`studio.consistencyMode.${m}`)}
                  </button>
                ))}
              </div>

              {comicMode === 'manual' ? (
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  {panels.map((p, i) => (
                    <MentionTextarea
                      key={i}
                      value={p}
                      onChange={(v) => setPanels((cur) => cur.map((c, ci) => (ci === i ? v : c)))}
                      rows={3}
                      className="text-sm"
                      hintPhrases={[t('studio.comic.panelPlaceholder', { n: i + 1 })]}
                    />
                  ))}
                </div>
              ) : (
                <div>
                  <MentionTextarea
                    value={story}
                    onChange={setStory}
                    rows={4}
                    hintPhrases={[t('studio.comic.storyPlaceholder')]}
                  />
                  <CharCount value={story} max={capabilities.data?.image.max_prompt_chars} />
                </div>
              )}
            </div>
          )}

          {tab === 'image.sequence' && (
            <div className="space-y-3">
              <div className="flex gap-2 text-sm">
                {(['quick', 'continuity'] as const).map((m) => (
                  <button
                    key={m}
                    onClick={() => setImageSequenceMode(m)}
                    title={t(`studio.consistencyMode.${m}Tooltip`)}
                    className={`rounded-lg border px-3 py-1.5 ${
                      imageSequenceMode === m ? 'border-violet-500 bg-violet-500/10 text-violet-300' : 'border-zinc-800 text-zinc-400 hover:border-zinc-700'
                    }`}
                  >
                    {t(`studio.consistencyMode.${m}`)}
                  </button>
                ))}
              </div>
              <ShotList
                shots={shots}
                setShots={setShots}
                sourceRefs={shotSourceRefs}
                setSourceRefs={setShotSourceRefs}
                placeholder={(i) => t('studio.imageSequence.shotPlaceholder', { n: i + 1 })}
                addLabel={t('studio.imageSequence.addLabel')}
              />
            </div>
          )}

          {tab === 'video.single' && (
            <div className="space-y-4">
              <div>
                <MentionTextarea
                  value={vText}
                  onChange={setVText}
                  rows={3}
                  placeholder={t('studio.examples.foxVideo')}
                  onMentionAsset={(asset) => {
                    // video.single is the one workflow with real multi-slot
                    // reference arrays (F6.1-F6.4) — a #-mentioned asset
                    // joins whichever list matches its type, deduped, and
                    // switches refMode so the AssetPicker below reflects it
                    // immediately instead of silently holding a reference
                    // the visible UI doesn't show as selected.
                    if (refMode === 'firstLast') return
                    setRefMode('reference')
                    if (asset.type === 'video') {
                      setRefVideoIds((cur) => (cur.includes(asset.biz_id) ? cur : [...cur, asset.biz_id]))
                    } else {
                      setRefImageIds((cur) => (cur.includes(asset.biz_id) ? cur : [...cur, asset.biz_id]))
                    }
                  }}
                />
                <CharCount value={vText} max={capabilities.data?.video.max_prompt_chars} />
              </div>

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
                  // Manual #-overrides reference shots by position — a
                  // reorder would silently point them at the wrong shot, so
                  // this clears them rather than risk a wrong-but-plausible
                  // reference surviving the move.
                  setVsShotReferenceOverrides((cur) => cur.map(() => null))
                }}
              >
                <SortableContext items={vsShots.map((s) => s.id)} strategy={verticalListSortingStrategy}>
                  {vsShots.map((shot, i) => (
                    <SortableShotRow
                      key={shot.id}
                      shot={shot}
                      index={i}
                      mode={shotMode(i + 1, vsRecalibrateEvery, characterSlotIds.some(Boolean))}
                      onChange={(text) => setVsShots((cur) => cur.map((s) => (s.id === shot.id ? { ...s, text } : s)))}
                      onRemove={() => {
                        setVsShots((cur) => cur.filter((s) => s.id !== shot.id))
                        setVsShotReferenceOverrides((cur) =>
                          cur
                            .filter((_, ci) => ci !== i)
                            .map((r) => (r == null ? r : r === i + 1 ? null : r > i + 1 ? r - 1 : r)),
                        )
                      }}
                      removable={vsShots.length > 1}
                      earlierShots={vsShots.slice(0, i).map((s, idx) => ({ index: idx + 1, text: s.text }))}
                      referenceOverride={vsShotReferenceOverrides[i] ?? null}
                      onSetReferenceOverride={(shotIndex) =>
                        setVsShotReferenceOverrides((cur) => cur.map((r, ri) => (ri === i ? shotIndex : r)))
                      }
                      onClearReferenceOverride={() =>
                        setVsShotReferenceOverrides((cur) => cur.map((r, ri) => (ri === i ? null : r)))
                      }
                    />
                  ))}
                </SortableContext>
              </DndContext>
              <button
                onClick={() => {
                  setVsShots((cur) => [...cur, { id: crypto.randomUUID(), text: '' }])
                  setVsShotReferenceOverrides((cur) => [...cur, null])
                }}
                className="text-sm text-violet-400 hover:text-violet-300"
              >
                {t('studio.addSegment')}
              </button>
              {vsShots.length > 4 && (
                <p className="text-xs text-amber-500">{t('studio.driftWarning', { n: vsRecalibrateEvery })}</p>
              )}

              <div className="pt-2">
                <Capsule>
                  <span className="text-zinc-500">{t('studio.narrativeContinuity.label')}</span>
                  <input
                    type="checkbox"
                    checked={narrativeContinuity}
                    onChange={(e) => setNarrativeContinuity(e.target.checked)}
                    className="accent-violet-500"
                  />
                </Capsule>
                {narrativeContinuity && (
                  <div className="mt-2 flex flex-wrap gap-1.5" title={t('studio.narrativeContinuity.modeTooltip')}>
                    {(['window', 'manual', 'smart'] as const).map((m) => (
                      <button
                        key={m}
                        onClick={() => setVsReferenceSelectionMode(m)}
                        className={`rounded-lg px-3 py-1.5 text-sm transition ${
                          vsReferenceSelectionMode === m ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-400 hover:bg-zinc-900'
                        }`}
                      >
                        {t(`studio.narrativeContinuity.mode.${m}`)}
                      </button>
                    ))}
                  </div>
                )}
              </div>

              <div className="pt-2">
                <p className="mb-2 text-xs uppercase tracking-wide text-zinc-500" title={t('studio.videoSequence.anchorRefTooltip')}>
                  {t('studio.videoSequence.anchorRef')}
                </p>
                <div className="space-y-2">
                  <div>
                    <p className="mb-1 text-xs text-zinc-600">{t('studio.refImages')}</p>
                    <AssetPicker
                      type="image"
                      selected={sourceImageId ? [sourceImageId] : []}
                      onToggle={(id) => {
                        setSourceImageId((cur) => (cur === id ? '' : id))
                        setSourceVideoId('')
                      }}
                      max={1}
                    />
                  </div>
                  <div>
                    <p className="mb-1 text-xs text-zinc-600">{t('studio.refVideos')}</p>
                    <AssetPicker
                      type="video"
                      selected={sourceVideoId ? [sourceVideoId] : []}
                      onToggle={(id) => {
                        setSourceVideoId((cur) => (cur === id ? '' : id))
                        setSourceImageId('')
                      }}
                      max={1}
                    />
                  </div>
                </div>
              </div>
            </div>
          )}

          {/* ── Capsule parameter row ──────────────────────────── */}
          <div className="flex flex-wrap items-center gap-2 border-t border-zinc-800 pt-4">
            {tab === 'image.batch' && (
              <Capsule>
                <span className="text-zinc-500">{t('studio.capsule.count')}</span>
                <select value={n} onChange={(e) => setN(Number(e.target.value))} className="bg-transparent text-zinc-100 outline-none">
                  {batchNOptions.map((v) => (
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
                  {durationOptions.map((v) => (
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
                  {resolutionOptions.map((r) => (
                    <option key={r} value={r} className="bg-zinc-900">
                      {r}
                    </option>
                  ))}
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
                  {ratioOptions.map((r) => (
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
                    {ratioOptions.map((r) => (
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
                <Capsule title={t('studio.capsule.skipPreviewTooltip')}>
                  <label className="flex cursor-pointer items-center gap-1.5 text-zinc-300">
                    <input
                      type="checkbox"
                      checked={vsSkipPreview}
                      onChange={(e) => setVsSkipPreview(e.target.checked)}
                      className="accent-violet-500"
                    />
                    {t('studio.capsule.skipPreview')}
                  </label>
                </Capsule>
              </>
            )}

            {!isGuest && (
              <>
                {characterSlotIds.map((id, i) => (
                  <Capsule key={i}>
                    <span className="text-zinc-500">{t('studio.capsule.characterSlot', { letter: SLOT_LETTERS[i] })}</span>
                    <CharacterSlotPicker
                      value={id}
                      onChange={(v) =>
                        setCharacterSlotIds((cur) => cur.map((cur_id, cur_i) => (cur_i === i ? v : cur_id)))
                      }
                      options={characters.data?.characters ?? []}
                    />
                    {characterSlotIds.length > 1 && (
                      <button
                        type="button"
                        onClick={() => setCharacterSlotIds((cur) => cur.filter((_, cur_i) => cur_i !== i))}
                        title={t('common.delete')}
                        className="text-zinc-600 transition hover:text-red-400"
                      >
                        ✕
                      </button>
                    )}
                  </Capsule>
                ))}
                {characterSlotIds.length < MAX_CHARACTER_SLOTS && (
                  <button
                    type="button"
                    onClick={() => setCharacterSlotIds((cur) => [...cur, ''])}
                    className="rounded-full border border-dashed border-zinc-700 px-3 py-1.5 text-xs text-zinc-500 transition hover:border-violet-500 hover:text-violet-300"
                  >
                    {t('studio.capsule.addCharacter')}
                  </button>
                )}
                {!!projects.data?.projects.length && (
                  <Capsule title={t('studio.capsule.projectTooltip')}>
                    <span className="text-zinc-500">{t('studio.capsule.project')}</span>
                    <select value={projectId} onChange={(e) => setProjectId(e.target.value)} className="bg-transparent text-zinc-100 outline-none">
                      <option value="" className="bg-zinc-900">
                        {t('studio.unselected')}
                      </option>
                      {projects.data.projects.map((p) => (
                        <option key={p.biz_id} value={p.biz_id} className="bg-zinc-900">
                          {p.name}
                        </option>
                      ))}
                    </select>
                  </Capsule>
                )}
              </>
            )}

            <div className="flex-1" />

            {!isGuest ? (
              <>
                <div className="relative text-right font-mono text-sm text-zinc-300">
                  <button
                    type="button"
                    onClick={() => setShowBreakdown((v) => !v)}
                    className="hover:text-zinc-100"
                    title={t('studio.breakdown.toggle')}
                  >
                    <span className="text-violet-400">✦</span> <AnimatedNumber value={estimate} />
                    {upgradeReferencePrice !== null && upgradeReferencePrice > estimate && (
                      <s className="ml-1.5 text-zinc-600">{upgradeReferencePrice}</s>
                    )}
                  </button>
                  {showBreakdown && estimateItems.length > 0 && (
                    <div className="absolute bottom-full right-0 z-10 mb-2 w-56 rounded-xl border border-zinc-800 bg-zinc-900 p-3 text-left shadow-xl">
                      <p className="mb-2 text-[11px] uppercase tracking-wide text-zinc-500">{t('studio.breakdown.title')}</p>
                      <div className="space-y-1">
                        {estimateItems.map((it, i) => (
                          <div key={i} className="flex items-center justify-between text-xs text-zinc-300">
                            <span>
                              {t(`studio.breakdown.item.${it.kind}`)}
                              {it.count > 1 ? ` ×${it.count}` : ''}
                            </span>
                            <span className="font-mono">✦{it.credits}</span>
                          </div>
                        ))}
                      </div>
                      {upgradeReferencePrice !== null && upgradeReferencePrice > estimate && (
                        <p className="mt-2 border-t border-zinc-800 pt-2 text-[11px] text-zinc-500">
                          {t('studio.breakdown.upgradeHint', { price: upgradeReferencePrice })}
                        </p>
                      )}
                    </div>
                  )}
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
              <button
                onClick={runTrial}
                disabled={trialBusy || !text.trim()}
                className="rounded-full bg-violet-500 px-5 py-2 text-sm font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
              >
                {trialBusy ? t('studio.generating') : t('studio.trial.tryOnce')}
              </button>
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
            <div className="space-y-2">
              <div className="flex flex-wrap items-center gap-1.5">
                {uniqueStyleTypes(presets.data.presets).map((st) => (
                  <button
                    key={st}
                    onClick={() => setStyleFilter((cur) => (cur === st ? '' : st))}
                    className={`rounded-full border px-2.5 py-1 text-xs transition ${
                      styleFilter === st ? 'border-violet-500 bg-violet-500/10 text-violet-300' : 'border-zinc-800 text-zinc-400 hover:border-zinc-700'
                    }`}
                  >
                    {st}
                  </button>
                ))}
                <div className="flex-1" />
                {savingPreset ? (
                  <div className="flex items-center gap-1.5">
                    <input
                      value={newPresetName}
                      onChange={(e) => setNewPresetName(e.target.value)}
                      placeholder={t('studio.savePreset.namePlaceholder')}
                      className="w-32 rounded-lg border border-zinc-800 bg-zinc-950 px-2 py-1 text-xs outline-none focus:border-violet-500"
                    />
                    <button
                      onClick={() => savePreset.mutate()}
                      disabled={!newPresetName.trim() || savePreset.isPending}
                      className="rounded-lg bg-violet-500 px-2 py-1 text-xs text-white disabled:opacity-50"
                    >
                      {t('common.save')}
                    </button>
                    <button onClick={() => setSavingPreset(false)} className="text-xs text-zinc-500 hover:text-zinc-300">
                      {t('common.cancel')}
                    </button>
                  </div>
                ) : (
                  <button onClick={() => setSavingPreset(true)} className="text-xs text-violet-400 hover:text-violet-300">
                    + {t('studio.savePreset.button')}
                  </button>
                )}
              </div>
              <PresetCarousel
                presets={styleFilter ? presets.data.presets.filter((p) => p.style_type === styleFilter) : presets.data.presets}
                selected={presetIds}
                onToggle={(id) => setPresetIds((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]))}
                onDelete={handleDeletePreset}
                deletingId={deletingPresetId}
              />
            </div>
          )}
        </div>

        {bizId && (
          <Link to={`/jobs/${bizId}`} className="text-sm text-violet-400 hover:text-violet-300">
            {t('studio.viewGraph')}
          </Link>
        )}

        {/* ── Results ─────────────────────────────────────────── */}
        <div className="min-h-80 rounded-2xl border border-zinc-800 bg-zinc-900/40 p-6">
          {!bizId && !isGuest && (
            <HomeFeed
              presets={presets.data?.presets ?? []}
              onPickFragment={(fragment) => (tab === 'video.single' ? setVText(fragment) : setText(fragment))}
              onTogglePreset={(id) => setPresetIds((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]))}
              selectedPresetIds={presetIds}
            />
          )}

          {!bizId && isGuest && (
            <div className="mx-auto max-w-md space-y-3 text-center">
              {!trialImageUrl && !trialError && <p className="text-sm text-zinc-400">{t('studio.trial.intro')}</p>}
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
              <p className="text-xs text-zinc-500">
                {t('studio.estimatedWait', {
                  minutes: formatWaitMinutes(
                    estimateWaitSeconds(tab, {
                      n,
                      shots: tab === 'image.sequence' ? shots.filter((s) => s.trim()).length : vsShots.filter((s) => s.text.trim()).length,
                      durationSeconds: tab === 'video.single' ? duration : vsDuration,
                      resolution,
                    }),
                  ),
                })}
              </p>
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

// §07's "字數計數" gap — max is undefined until GET /capabilities resolves,
// in which case this just shows the raw count with no "/limit" suffix
// rather than a misleading placeholder number.
function CharCount({ value, max }: { value: string; max?: number }) {
  const over = max !== undefined && value.length > max
  return (
    <p className={`mt-1 text-right text-[11px] ${over ? 'text-red-400' : 'text-zinc-600'}`}>
      {value.length}
      {max !== undefined ? `/${max}` : ''}
    </p>
  )
}

function uniqueStyleTypes(presets: { style_type: string }[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const p of presets) {
    if (p.style_type && !seen.has(p.style_type)) {
      seen.add(p.style_type)
      out.push(p.style_type)
    }
  }
  return out
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
  earlierShots,
  referenceOverride,
  onSetReferenceOverride,
  onClearReferenceOverride,
}: {
  shot: ShotItem
  index: number
  mode: ShotMode
  onChange: (text: string) => void
  onRemove: () => void
  removable: boolean
  // video.sequence's own #-mention override (Spec.ShotReferenceOverrides
  // doc) — same pattern image.sequence's ShotList already uses, kept here
  // rather than replaced by narrativeContinuity's automatic modes: "引用#
  // 這個本身是個值得的功能，需要你保留" was explicit.
  earlierShots: { index: number; text: string }[]
  referenceOverride: number | null
  onSetReferenceOverride: (shotIndex: number) => void
  onClearReferenceOverride: () => void
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
        title={mode}
        className={`shrink-0 rounded-full border px-2 py-1 text-[11px] ${SHOT_MODE_CLASS[mode]}`}
      >
        {t(SHOT_MODE_LABEL_KEY[mode])}
      </span>
      <div className="flex-1">
        <MentionTextarea
          value={shot.text}
          onChange={onChange}
          rows={2}
          className="text-sm"
          hintPhrases={[t('studio.videoSequence.shotPlaceholder', { n: index + 1 })]}
          siblingShots={earlierShots}
          onMentionShot={onSetReferenceOverride}
          shotLabelKey="studio.mention.videoShot"
        />
        {referenceOverride != null && (
          <p className="mt-1 flex items-center gap-1 text-xs text-violet-400">
            {t('studio.videoSequence.linkedToShot', { n: referenceOverride })}
            <button type="button" onClick={onClearReferenceOverride} className="text-zinc-500 hover:text-red-400">
              ×
            </button>
          </p>
        )}
      </div>
      {removable && (
        <button
          onClick={onRemove}
          className="h-fit shrink-0 rounded-lg border border-zinc-800 px-2 py-1.5 text-zinc-500 hover:text-red-400"
        >
          ×
        </button>
      )}
    </div>
  )
}

// filterShotsWithRefs drops blank shots (buildSpec's existing behavior)
// while keeping shotSourceRefs' 1-based indices valid: every kept shot's
// position can shift, so a ref pointing past a dropped shot gets
// renumbered, and a ref whose target was itself blank (and so got dropped)
// is orphaned back to 0 rather than silently pointing at the wrong shot.
function filterShotsWithRefs(shots: string[], sourceRefs: (number | null)[]): { shots: string[]; refs: number[] } {
  const keepIndices: number[] = []
  shots.forEach((s, i) => {
    if (s.trim()) keepIndices.push(i)
  })
  const oldToNew = new Map<number, number>() // 1-based old index -> 1-based new index
  keepIndices.forEach((oldIdx, newPos) => oldToNew.set(oldIdx + 1, newPos + 1))
  return {
    shots: keepIndices.map((i) => shots[i]),
    refs: keepIndices.map((i) => {
      const r = sourceRefs[i]
      return r ? (oldToNew.get(r) ?? 0) : 0
    }),
  }
}

function ShotList({
  shots,
  setShots,
  sourceRefs,
  setSourceRefs,
  placeholder,
  addLabel,
}: {
  shots: string[]
  setShots: React.Dispatch<React.SetStateAction<string[]>>
  sourceRefs: (number | null)[]
  setSourceRefs: React.Dispatch<React.SetStateAction<(number | null)[]>>
  placeholder: (i: number) => string
  addLabel: string
}) {
  const { t } = useTranslation()
  return (
    <div className="space-y-2">
      {shots.map((s, i) => (
        <div key={i} className="flex gap-2">
          <div className="flex-1">
            <MentionTextarea
              value={s}
              onChange={(v) => setShots((cur) => cur.map((c, ci) => (ci === i ? v : c)))}
              rows={2}
              className="text-sm"
              hintPhrases={[placeholder(i)]}
              siblingShots={shots.slice(0, i).map((text, idx) => ({ index: idx + 1, text }))}
              onMentionShot={(shotIndex) => setSourceRefs((cur) => cur.map((r, ri) => (ri === i ? shotIndex : r)))}
            />
            {sourceRefs[i] != null && (
              <p className="mt-1 flex items-center gap-1 text-xs text-violet-400">
                {t('studio.imageSequence.linkedToShot', { n: sourceRefs[i] })}
                <button
                  type="button"
                  onClick={() => setSourceRefs((cur) => cur.map((r, ri) => (ri === i ? null : r)))}
                  className="text-zinc-500 hover:text-red-400"
                >
                  ×
                </button>
              </p>
            )}
          </div>
          {shots.length > 1 && (
            <button
              onClick={() => {
                setShots((cur) => cur.filter((_, ci) => ci !== i))
                setSourceRefs((cur) =>
                  cur.filter((_, ci) => ci !== i).map((r) => (r == null ? r : r === i + 1 ? null : r > i + 1 ? r - 1 : r)),
                )
              }}
              className="h-fit shrink-0 rounded-lg border border-zinc-800 px-2 py-1.5 text-zinc-500 hover:text-red-400"
            >
              ×
            </button>
          )}
        </div>
      ))}
      <button
        onClick={() => {
          setShots((cur) => [...cur, ''])
          setSourceRefs((cur) => [...cur, null])
        }}
        className="text-sm text-violet-400 hover:text-violet-300"
      >
        {addLabel}
      </button>
    </div>
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
