import { useEffect, useState, type ReactNode } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { api, ApiError, type Spec, type WorkflowName, type JobResponse } from '../lib/api'
import {
  estimateImageCredits,
  estimateVideoCredits,
  estimatePromptEnhanceCredits,
  estimateStorySplitCredits,
} from '../lib/pricing'
import { videoSingleSchema, RATIO_VALUES } from '../lib/videoSpec'
import { resultAssetIds, WORKFLOW_LABEL, type Tab } from '../lib/jobResult'
import { displayNodeError, firstSpecificError } from '../lib/errors'
import { suggestActions, type SuggestedAction } from '../lib/suggestions'
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

// PRD §19.4.1's full creation studio, now mounted at `/` per §19.3 ("创作台
// （首页，登录/匿名皆可进入）") instead of behind a login wall — this is
// the single biggest information-architecture gap the jimeng comparison
// surfaced: the old build redirected `/` straight to `/login`, so the
// anonymous trial (F1.2, POST /trial/image, real endpoint since W1) was
// only reachable from a card buried on the login screen. Guests now land
// here directly; anything that needs an authed GET (characters/presets/me)
// is simply not fetched (`enabled: !isGuest`) rather than erroring, and the
// composer degrades to "compose + one free trial" instead of vanishing.
//
// Structural change from the tab-sidebar build: six workflows collapse into
// one composer (headline dropdown + quick-switch cards drive the same `tab`
// state) with a capsule parameter row, mirroring jimeng's single-input
// pattern (§19.0①) instead of six parallel forms behind six sidebar tabs.
// Every Spec field the old build could set, this one still can — see the
// blueprint's capsule → Spec field table.

const TAB_META: Record<Tab, { icon: string; blurb: string }> = {
  'image.single': { icon: '🖼', blurb: '一句话生成一张图' },
  'image.batch': { icon: '▦', blurb: '同一句话一次出多张' },
  'image.comic4': { icon: '🗯', blurb: '共享角色与风格的四格' },
  'image.sequence': { icon: '⛓', blurb: '同角色连续出一组图' },
  'video.single': { icon: '🎬', blurb: '文生视频或图生视频' },
  'video.sequence': { icon: '🎞', blurb: '768P 预览后再定稿 2K' },
}
const TABS = Object.keys(TAB_META) as Tab[]

// F6.5's "双保险": videoSpec.ts's zod schema is still the structural second
// guard checked right before submit — this is the first guard, and it's now
// enforced by construction (only one panel can ever be mounted) instead of
// by graying out whichever panel lost the race, which is what jimeng's
// single "全能参考" dropdown gets right that two parallel opacity-40 panels
// don't: there's no state where the user has to read a tooltip to find out
// why something is disabled.
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
  const accessToken = useAuthStore((s) => s.accessToken)
  const isGuest = !accessToken
  const navigate = useNavigate()

  const [tab, setTab] = useState<Tab>('image.single')
  const [text, setText] = useState('一只狐狸站在雪地上，水彩风格')
  const [n, setN] = useState(4)
  const [panels, setPanels] = useState(['', '', '', ''])
  const [shots, setShots] = useState([''])
  const [slotA, setSlotA] = useState('')
  const [slotB, setSlotB] = useState('')
  const [presetIds, setPresetIds] = useState<string[]>([])
  const [bizId, setBizId] = useState<string | null>(null)

  // video.single-only state (F6.1-F6.5).
  const [vText, setVText] = useState('镜头缓缓推进，一只狐狸转身望向镜头，雪花飘落')
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
  const [vsShots, setVsShots] = useState([''])
  const [vsDuration, setVsDuration] = useState(5)
  const [vsRatio, setVsRatio] = useState<(typeof RATIO_VALUES)[number]>('16:9')
  const [vsRecalibrateEvery, setVsRecalibrateEvery] = useState(3)

  const me = useMe(!isGuest)
  const characters = useCharacters(!isGuest)
  const presets = usePresets(!isGuest)
  const pushToast = useToast()

  // F1.2's anonymous trial — relocated here from the login page (§19.0's
  // "先给价值再要注册" only works if the value is visible before the wall,
  // not after it). Self-contained, doesn't touch jobs/credits/assets.
  const [trialPrompt, setTrialPrompt] = useState('两人在天台对峙，黄昏逆光，风很大')
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
      setTrialError(err instanceof ApiError ? err.message : '生成失败，请重试')
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

  const estimate =
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
              : estimateVideoCredits(vsDuration, '768P') * (vsShots.filter((s) => s.trim()).length || 1)

  const balance = me.data?.balance ?? 0
  const insufficientBalance = !isGuest && me.isSuccess && balance < estimate

  const createJob = useMutation({
    mutationFn: () => {
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
          if (!story.trim()) throw new Error('请输入剧情描述')
          spec.story = story
        } else {
          spec.panels = panels
        }
      } else if (tab === 'image.sequence') {
        spec.shots = shots.filter((s) => s.trim())
      } else if (tab === 'video.single') {
        if (!videoValidation.success) {
          throw new Error(videoValidation.error.issues[0]?.message ?? '参数不合法')
        }
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
        const trimmedShots = vsShots.filter((s) => s.trim())
        if (trimmedShots.length === 0) throw new Error('至少需要一段镜头描述')
        spec.shots = trimmedShots
        spec.duration_seconds = vsDuration
        spec.ratio = vsRatio
        spec.recalibrate_every = vsRecalibrateEvery
      }
      const idemKey = crypto.randomUUID()
      return api.createJob(tab as WorkflowName, spec, idemKey)
    },
    onSuccess: (res) => {
      setBizId(res.biz_id)
      me.refetch()
    },
    onError: () => pushToast('提交失败，请重试', () => createJob.mutate()),
  })

  const jobStream = useJobStream(bizId)
  const job = jobStream.data as JobResponse | undefined

  useEffect(() => {
    if (jobStream.isError) {
      pushToast('网络连接不稳定，无法获取作业状态', () => jobStream.refetch())
    }
  }, [jobStream.isError, jobStream.refetch, pushToast])

  const assetIds = resultAssetIds(job, tab)
  const running = !!bizId && job?.status !== 'succeeded' && job?.status !== 'failed'
  const gateNode = job?.nodes.find((n) => n.name === 'gate')
  const gateSuspended = tab === 'video.sequence' && gateNode?.phase === 'Suspended'
  const suggestions = job && job.status === 'succeeded' ? suggestActions(job, tab, assetIds) : []

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
        setVsShots(action.shots.length ? action.shots : [''])
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
            开启你的{' '}
            <select
              value={tab}
              onChange={(e) => setTab(e.target.value as Tab)}
              className="appearance-none border-b-2 border-dashed border-violet-500/60 bg-transparent px-1 text-violet-400 outline-none"
            >
              {TABS.map((t) => (
                <option key={t} value={t} className="bg-zinc-900 text-zinc-100">
                  {WORKFLOW_LABEL[t]}
                </option>
              ))}
            </select>{' '}
            即刻创作
          </h1>
          {isGuest && <p className="mt-2 text-sm text-zinc-500">先免费试用一次，注册后解锁全部形态与素材库</p>}
        </header>

        {/* ── Composer ─────────────────────────────────────────── */}
        <div className="space-y-4 rounded-2xl border border-zinc-800 bg-zinc-900/60 p-5">
          {(tab === 'image.single' || tab === 'image.batch') && (
            <textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              rows={3}
              className="w-full resize-none rounded-xl border border-zinc-800 bg-zinc-950 p-4 outline-none focus:border-violet-500"
              placeholder="两人在天台对峙，黄昏逆光，风很大"
            />
          )}

          {tab === 'image.single' && (
            <div>
              <p className="mb-2 text-xs uppercase tracking-wide text-zinc-500">参考图（可选，图生图）</p>
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
                  逐格手写
                </button>
                <button
                  onClick={() => setComicMode('auto')}
                  className={`rounded-lg px-3 py-1.5 ${comicMode === 'auto' ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-400 hover:bg-zinc-950'}`}
                  title="用 AI 把一段剧情自动拆成 4 格画面描述"
                >
                  剧情自动拆 4 格
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
                      placeholder={`格 ${i + 1}`}
                    />
                  ))}
                </div>
              ) : (
                <textarea
                  value={story}
                  onChange={(e) => setStory(e.target.value)}
                  rows={4}
                  className="w-full resize-none rounded-xl border border-zinc-800 bg-zinc-950 p-4 outline-none focus:border-violet-500"
                  placeholder="一段完整的剧情描述，系统会自动拆成 4 个连续分镜"
                />
              )}
            </div>
          )}

          {tab === 'image.sequence' && (
            <ShotList
              shots={shots}
              setShots={setShots}
              placeholder={(i) => `第 ${i + 1} 张`}
              addLabel="+ 添加一张"
            />
          )}

          {tab === 'video.single' && (
            <div className="space-y-4">
              <textarea
                value={vText}
                onChange={(e) => setVText(e.target.value)}
                rows={3}
                className="w-full resize-none rounded-xl border border-zinc-800 bg-zinc-950 p-4 outline-none focus:border-violet-500"
                placeholder="镜头缓缓推进，一只狐狸转身望向镜头，雪花飘落"
              />

              <div>
                <Capsule>
                  <span className="text-zinc-500">参考模式</span>
                  <select
                    value={refMode}
                    onChange={(e) => setRefModeAndClear(e.target.value as RefMode)}
                    className="bg-transparent text-zinc-100 outline-none"
                  >
                    <option value="none" className="bg-zinc-900">
                      不使用
                    </option>
                    <option value="firstLast" className="bg-zinc-900">
                      首尾帧
                    </option>
                    <option value="reference" className="bg-zinc-900">
                      参考素材
                    </option>
                  </select>
                </Capsule>

                {refMode === 'firstLast' && (
                  <div className="mt-3 space-y-2">
                    <div>
                      <p className="mb-1 text-xs text-zinc-600">首帧</p>
                      <AssetPicker
                        type="image"
                        selected={firstFrameAssetId ? [firstFrameAssetId] : []}
                        onToggle={(id) => setFirstFrameAssetId((cur) => (cur === id ? '' : id))}
                        max={1}
                      />
                    </div>
                    <div>
                      <p className="mb-1 text-xs text-zinc-600">尾帧</p>
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
                      <p className="mb-1 text-xs text-zinc-600">参考图片</p>
                      <AssetPicker
                        type="image"
                        selected={refImageIds}
                        onToggle={(id) =>
                          setRefImageIds((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]))
                        }
                      />
                    </div>
                    <div>
                      <p className="mb-1 text-xs text-zinc-600">参考视频</p>
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

              <label
                className="flex items-center gap-2 text-sm text-zinc-400"
                title="生成前用 AI 深度理解并润色你的提示词，按用量额外计费"
              >
                <input
                  type="checkbox"
                  checked={promptEnhance}
                  onChange={(e) => setPromptEnhance(e.target.checked)}
                  className="accent-violet-500"
                />
                AI 提示词增强（+约 {estimatePromptEnhanceCredits()} 积分）
              </label>
            </div>
          )}

          {tab === 'video.sequence' && (
            <ShotList
              shots={vsShots}
              setShots={setVsShots}
              placeholder={(i) => `第 ${i + 1} 段镜头描述`}
              addLabel="+ 添加一段"
            />
          )}

          {/* ── Capsule parameter row ──────────────────────────── */}
          <div className="flex flex-wrap items-center gap-2 border-t border-zinc-800 pt-4">
            {tab === 'image.batch' && (
              <Capsule>
                <span className="text-zinc-500">数量</span>
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
                <span className="text-zinc-500">时长</span>
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
                <span className="text-zinc-500">解析度</span>
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
              <Capsule disabled={refMode !== 'none'} title={refMode !== 'none' ? '首尾帧/参考素材模式下画面比例由素材决定' : undefined}>
                <span className="text-zinc-500">比例</span>
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
                  <span className="text-zinc-500">比例</span>
                  <select value={vsRatio} onChange={(e) => setVsRatio(e.target.value as (typeof RATIO_VALUES)[number])} className="bg-transparent text-zinc-100 outline-none">
                    {RATIO_VALUES.map((r) => (
                      <option key={r} value={r} className="bg-zinc-900">
                        {r}
                      </option>
                    ))}
                  </select>
                </Capsule>
                <Capsule title="每 N 段重新锚定一次角色参考图（r2va），其余段落用前一段尾帧续接（i2va）">
                  <span className="text-zinc-500">锚定间隔</span>
                  <select
                    value={vsRecalibrateEvery}
                    onChange={(e) => setVsRecalibrateEvery(Number(e.target.value))}
                    className="bg-transparent text-zinc-100 outline-none"
                  >
                    {[2, 3, 4, 5].map((v) => (
                      <option key={v} value={v} className="bg-zinc-900">
                        每 {v} 段
                      </option>
                    ))}
                  </select>
                </Capsule>
              </>
            )}

            {!isGuest && (
              <>
                <Capsule>
                  <span className="text-zinc-500">角色 A</span>
                  <CharacterSelectInline value={slotA} onChange={setSlotA} options={characters.data?.characters ?? []} />
                </Capsule>
                <Capsule>
                  <span className="text-zinc-500">角色 B</span>
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
                  {createJob.isPending ? '提交中…' : running ? '生成中…' : '生成 →'}
                </button>
              </>
            ) : (
              <Link to="/login" className="rounded-full bg-violet-500 px-5 py-2 text-sm font-medium text-white transition hover:bg-violet-400">
                登录后生成 →
              </Link>
            )}
          </div>

          {insufficientBalance && <p className="text-sm text-red-400">积分不足（余额 {balance}）</p>}
          {tab === 'video.single' && !videoValidation.success && (
            <p className="text-sm text-amber-400">{videoValidation.error.issues[0]?.message}</p>
          )}

          {!isGuest && characters.isSuccess && characters.data.characters.length === 0 && (
            <p className="text-xs text-zinc-600">还没有角色，去「角色库」创建</p>
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
          {TABS.map((t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={`rounded-xl border p-3 text-left transition ${
                tab === t ? 'border-violet-500 bg-violet-500/10' : 'border-zinc-800 bg-zinc-900/40 hover:border-zinc-700'
              }`}
            >
              <p className="text-lg leading-none">{TAB_META[t].icon}</p>
              <p className={`mt-1.5 text-sm font-medium ${tab === t ? 'text-violet-300' : 'text-zinc-200'}`}>
                {WORKFLOW_LABEL[t]}
              </p>
              <p className="mt-0.5 text-xs text-zinc-500">{TAB_META[t].blurb}</p>
            </button>
          ))}
        </div>

        {bizId && (
          <Link to={`/jobs/${bizId}`} className="text-sm text-violet-400 hover:text-violet-300">
            查看流程图 →
          </Link>
        )}

        {/* ── Results ─────────────────────────────────────────── */}
        <div className="min-h-80 rounded-2xl border border-zinc-800 bg-zinc-900/40 p-6">
          {!bizId && !isGuest && (
            <EmptyState presets={presets.data?.presets ?? []} onPick={(fragment) => (tab === 'video.single' ? setVText(fragment) : setText(fragment))} />
          )}

          {!bizId && isGuest && (
            <div className="mx-auto max-w-md space-y-3 text-center">
              <p className="text-sm text-zinc-400">不用注册，先免费试用一次单图生成</p>
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
                {trialBusy ? '生成中…' : '✦ 匿名试用一次'}
              </button>
              {trialError && <p className="text-sm text-red-400">{trialError}</p>}
              {trialImageUrl && (
                <div className="pt-2">
                  <img src={trialImageUrl} alt="" className="mx-auto rounded-lg" />
                  <p className="mt-2 text-xs text-zinc-500">喜欢这张？登录后才能保存到素材库</p>
                </div>
              )}
            </div>
          )}

          {gateSuspended && bizId && job && (
            <PreviewGate bizId={bizId} job={job} duration={vsDuration} onResumed={() => jobStream.refetch()} />
          )}

          {running && !gateSuspended && (
            <div className="flex min-h-64 flex-col items-center justify-center gap-2">
              <GenerationProgress kind={tab.startsWith('video') ? 'video' : 'image'} />
              {jobStream.streamState === 'reconnecting' && (
                <p className="text-xs text-amber-500">实时连接不稳定，重新连接中…</p>
              )}
            </div>
          )}

          {job?.status === 'failed' && (
            <p className="text-center text-red-400">生成失败：{displayNodeError(job && firstSpecificError(job.nodes))}</p>
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
                  <span className="text-xs text-zinc-500">猜你想接着做：</span>
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

// §19.4.1's cold-start "灵感引导" empty state: reuses preset cover images
// (already fetched for the carousel above) instead of a bare "结果会显示在
// 这里" placeholder — clicking one drops its prompt fragment straight into
// the active input.
function EmptyState({ presets, onPick }: { presets: { biz_id: string; name: string; cover_url: string; prompt_fragment: string }[]; onPick: (fragment: string) => void }) {
  const sample = presets.slice(0, 4)
  if (sample.length === 0) {
    return <p className="text-center text-zinc-500">结果会显示在这里</p>
  }
  return (
    <div className="mx-auto max-w-md text-center">
      <p className="mb-3 text-sm text-zinc-500">猜你想生成：</p>
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
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)} className="bg-transparent text-zinc-100 outline-none">
      <option value="" className="bg-zinc-900">
        未选择
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
