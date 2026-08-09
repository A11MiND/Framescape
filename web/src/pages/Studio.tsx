import { useEffect, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type Spec, type WorkflowName, type JobResponse } from '../lib/api'
import {
  estimateImageCredits,
  estimateVideoCredits,
  estimatePromptEnhanceCredits,
  estimateStorySplitCredits,
} from '../lib/pricing'
import { videoSingleSchema, RATIO_VALUES } from '../lib/videoSpec'
import { resultAssetIds, WORKFLOW_LABEL, type Tab } from '../lib/jobResult'
import { displayNodeError, firstSpecificError } from '../lib/errors'
import { useToast } from '../components/Toast'
import Nav from '../components/Nav'
import { AssetPicker } from '../components/AssetPicker'
import PreviewGate from '../components/PreviewGate'
import GenerationProgress from '../components/GenerationProgress'

// PRD §19.4.1's full creation studio (3-column: form-tab nav + character
// slots + material upload / main result view / params + preset scroller) is
// a much larger build than this — this covers the functional core for the
// five forms (F5.1-F5.5 image + F6.1-F6.5 video.single + F6.7/F6.8
// video.sequence's preview gate): tab switch, character/preset selection,
// live client-side cost estimate, submit, poll, render results. Deferred
// deliberately: drag-drop material upload with role-tagging (F6.4 is P1 and
// covered indirectly via the asset picker, just not via drag-drop),
// predictive next-step recommendation cards (§19.4.2).

const TABS: { id: Tab; label: string }[] = (
  ['image.single', 'image.batch', 'image.comic4', 'image.sequence', 'video.single', 'video.sequence'] as Tab[]
).map((id) => ({ id, label: WORKFLOW_LABEL[id] }))

function useCharacters() {
  return useQuery({ queryKey: ['characters'], queryFn: api.listCharacters })
}
function usePresets() {
  return useQuery({ queryKey: ['presets'], queryFn: () => api.listPresets() })
}
function useMe() {
  return useQuery({ queryKey: ['me'], queryFn: api.me })
}

export default function Studio() {
  const [tab, setTab] = useState<Tab>('image.single')
  const [text, setText] = useState('一只狐狸站在雪地上，水彩风格')
  const [n, setN] = useState(4)
  const [panels, setPanels] = useState(['', '', '', ''])
  const [shots, setShots] = useState([''])
  const [slotA, setSlotA] = useState('')
  const [slotB, setSlotB] = useState('')
  const [presetIds, setPresetIds] = useState<string[]>([])
  const [bizId, setBizId] = useState<string | null>(null)

  // video.single-only state (F6.1-F6.5). Kept separate from `text` above —
  // it has its own 7000-char cap and shares nothing with the image forms.
  const [vText, setVText] = useState('镜头缓缓推进，一只狐狸转身望向镜头，雪花飘落')
  const [duration, setDuration] = useState(5)
  const [resolution, setResolution] = useState<'768P' | '2K'>('768P')
  const [ratio, setRatio] = useState<(typeof RATIO_VALUES)[number]>('16:9')
  const [firstFrameAssetId, setFirstFrameAssetId] = useState('')
  const [lastFrameAssetId, setLastFrameAssetId] = useState('')
  const [refImageIds, setRefImageIds] = useState<string[]>([])
  const [refVideoIds, setRefVideoIds] = useState<string[]>([])
  const [promptEnhance, setPromptEnhance] = useState(false)
  // image.single-only state (F5.8): optional image-to-image source.
  const [sourceImageId, setSourceImageId] = useState('')
  // image.comic4-only state (F5.4): auto-split one story into 4 panels
  // instead of writing each panel by hand.
  const [comicMode, setComicMode] = useState<'manual' | 'auto'>('manual')
  const [story, setStory] = useState('')

  // video.sequence-only state (F6.7/F6.8). The draft submission only needs
  // shots/duration/ratio/recalibrateEvery — resolution isn't asked here
  // because the draft is always 768P (createVideoSequence's own doc:
  // "预览门只预扣 768P 部分积分"); 2K only happens per-shot at the gate.
  const [vsShots, setVsShots] = useState([''])
  const [vsDuration, setVsDuration] = useState(5)
  const [vsRatio, setVsRatio] = useState<(typeof RATIO_VALUES)[number]>('16:9')
  const [vsRecalibrateEvery, setVsRecalibrateEvery] = useState(3)

  const me = useMe()
  const characters = useCharacters()
  const presets = usePresets()
  const pushToast = useToast()

  // F6.5's UI half of the double guard: picking either mode's material
  // greys out the other's picker entirely, so the two can't both end up
  // populated through the UI (videoSpec.ts's schema is the second guard,
  // checked right before submit).
  const hasFirstLast = firstFrameAssetId !== '' || lastFrameAssetId !== ''
  const hasRef = refImageIds.length > 0 || refVideoIds.length > 0

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
              : // video.sequence: draft is always 768P (jobsvc.createVideoSequence's
                // own hold formula) — the 2K delta only gets held later, at Resume,
                // for whichever shots the user actually upgrades at the gate.
                estimateVideoCredits(vsDuration, '768P') *
                (vsShots.filter((s) => s.trim()).length || 1)

  const balance = me.data?.balance ?? 0
  const insufficientBalance = me.isSuccess && balance < estimate

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
        if (!hasFirstLast && !hasRef) spec.ratio = ratio
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
      // A fresh key per submission — this isn't "retry the same logical
      // request", it's "the user pressed the button again"; the key only
      // needs to be stable *within* one submission's own retries, which
      // TanStack Query's mutation retry (if ever enabled) would reuse since
      // mutationFn is captured once per mutate() call.
      const idemKey = crypto.randomUUID()
      return api.createJob(tab as WorkflowName, spec, idemKey)
    },
    onSuccess: (res) => {
      setBizId(res.biz_id)
      me.refetch()
    },
    // §19.5.3's "提交后网络错误 → Toast（可重试）" — insufficient-balance is
    // already pre-empted by the inline block below (button disabled before
    // this ever fires), so a createJob failure here means an unexpected
    // submit-time error (network blip, a stale balance race) — exactly the
    // retryable-toast case, not a blocking one.
    onError: () => pushToast('提交失败，请重试', () => createJob.mutate()),
  })

  const jobQuery = useQuery<JobResponse>({
    queryKey: ['job', bizId],
    queryFn: () => api.getJob(bizId!),
    enabled: !!bizId,
    refetchInterval: (query) => {
      const status = query.state.data?.status
      return status === 'succeeded' || status === 'failed' ? false : 1500
    },
  })

  // Same §19.5.3 row, for the polling side: a run of failed GET /jobs polls
  // (not a "the job failed" — that's job.status === 'failed', shown inline
  // in the results card, never a toast per the table's "partial failure
  // doesn't interrupt" row) means we've lost the network mid-job.
  useEffect(() => {
    if (jobQuery.isError) {
      pushToast('网络连接不稳定，无法获取作业状态', () => jobQuery.refetch())
    }
  }, [jobQuery.isError, jobQuery.refetch, pushToast])

  const job = jobQuery.data
  const assetIds = resultAssetIds(job, tab)
  const running = !!bizId && job?.status !== 'succeeded' && job?.status !== 'failed'
  // video.sequence-only: the workflow suspends at `gate` once the 768P draft
  // chain finishes — job.status stays "running" throughout (only terminal
  // phases update it), so this is the only way to tell "still drafting" from
  // "waiting on the user's keep/redo/upgrade decision" apart.
  const gateNode = job?.nodes.find((n) => n.name === 'gate')
  const gateSuspended = tab === 'video.sequence' && gateNode?.phase === 'Suspended'

  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-50">
      <Nav />

      <div className="mx-auto flex max-w-5xl gap-6 px-6 py-8">
        {/* left: tab nav + character/preset selection */}
        <aside className="w-64 shrink-0 space-y-6">
          <nav className="space-y-1">
            {TABS.map((t) => (
              <button
                key={t.id}
                onClick={() => setTab(t.id)}
                className={`w-full rounded-lg px-3 py-2 text-left text-sm transition ${
                  tab === t.id ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-400 hover:bg-zinc-900'
                }`}
              >
                {t.label}
              </button>
            ))}
          </nav>

          <div>
            <p className="mb-2 text-xs uppercase tracking-wide text-zinc-500">角色槽</p>
            <div className="space-y-2">
              <CharacterSelect
                label="A"
                value={slotA}
                onChange={setSlotA}
                options={characters.data?.characters ?? []}
              />
              <CharacterSelect
                label="B"
                value={slotB}
                onChange={setSlotB}
                options={characters.data?.characters ?? []}
              />
            </div>
            {characters.isSuccess && characters.data.characters.length === 0 && (
              <p className="mt-1 text-xs text-zinc-600">还没有角色，去「角色库」创建</p>
            )}
          </div>

          <div>
            <p className="mb-2 text-xs uppercase tracking-wide text-zinc-500">预设</p>
            <div className="flex flex-wrap gap-1.5">
              {(presets.data?.presets ?? []).map((p) => {
                const active = presetIds.includes(p.biz_id)
                return (
                  <button
                    key={p.biz_id}
                    onClick={() =>
                      setPresetIds((cur) =>
                        active ? cur.filter((id) => id !== p.biz_id) : [...cur, p.biz_id],
                      )
                    }
                    className={`rounded-full border px-2.5 py-1 text-xs transition ${
                      active
                        ? 'border-violet-500 bg-violet-500/20 text-violet-300'
                        : 'border-zinc-800 text-zinc-400 hover:border-zinc-700'
                    }`}
                  >
                    {p.name}
                  </button>
                )
              })}
            </div>
          </div>
        </aside>

        {/* main: form + submit + results */}
        <main className="flex-1 space-y-4">
          {(tab === 'image.single' || tab === 'image.batch') && (
            <textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              rows={3}
              className="w-full resize-none rounded-xl border border-zinc-800 bg-zinc-900 p-4 outline-none focus:border-violet-500"
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

          {tab === 'image.batch' && (
            <label className="flex items-center gap-2 text-sm text-zinc-400">
              数量 n =
              <select
                value={n}
                onChange={(e) => setN(Number(e.target.value))}
                className="rounded-lg border border-zinc-800 bg-zinc-900 px-2 py-1"
              >
                {[2, 4, 6, 9].map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
            </label>
          )}

          {tab === 'image.comic4' && (
            <div className="space-y-3">
              <div className="flex gap-2 text-sm">
                <button
                  onClick={() => setComicMode('manual')}
                  className={`rounded-lg px-3 py-1.5 ${comicMode === 'manual' ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-400 hover:bg-zinc-900'}`}
                >
                  逐格手写
                </button>
                <button
                  onClick={() => setComicMode('auto')}
                  className={`rounded-lg px-3 py-1.5 ${comicMode === 'auto' ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-400 hover:bg-zinc-900'}`}
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
                      onChange={(e) =>
                        setPanels((cur) => cur.map((c, ci) => (ci === i ? e.target.value : c)))
                      }
                      rows={3}
                      className="resize-none rounded-xl border border-zinc-800 bg-zinc-900 p-3 text-sm outline-none focus:border-violet-500"
                      placeholder={`格 ${i + 1}`}
                    />
                  ))}
                </div>
              ) : (
                <textarea
                  value={story}
                  onChange={(e) => setStory(e.target.value)}
                  rows={4}
                  className="w-full resize-none rounded-xl border border-zinc-800 bg-zinc-900 p-4 outline-none focus:border-violet-500"
                  placeholder="一段完整的剧情描述，系统会自动拆成 4 个连续分镜"
                />
              )}
            </div>
          )}

          {tab === 'image.sequence' && (
            <div className="space-y-2">
              {shots.map((s, i) => (
                <div key={i} className="flex gap-2">
                  <input
                    value={s}
                    onChange={(e) =>
                      setShots((cur) => cur.map((c, ci) => (ci === i ? e.target.value : c)))
                    }
                    className="flex-1 rounded-lg border border-zinc-800 bg-zinc-900 px-3 py-2 text-sm outline-none focus:border-violet-500"
                    placeholder={`第 ${i + 1} 张`}
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
              <button
                onClick={() => setShots((cur) => [...cur, ''])}
                className="text-sm text-violet-400 hover:text-violet-300"
              >
                + 添加一张
              </button>
            </div>
          )}

          {tab === 'video.single' && (
            <div className="space-y-4">
              <textarea
                value={vText}
                onChange={(e) => setVText(e.target.value)}
                rows={3}
                className="w-full resize-none rounded-xl border border-zinc-800 bg-zinc-900 p-4 outline-none focus:border-violet-500"
                placeholder="镜头缓缓推进，一只狐狸转身望向镜头，雪花飘落"
              />

              <div className="flex flex-wrap items-center gap-4 text-sm text-zinc-400">
                <label className="flex items-center gap-2">
                  时长
                  <select
                    value={duration}
                    onChange={(e) => setDuration(Number(e.target.value))}
                    className="rounded-lg border border-zinc-800 bg-zinc-900 px-2 py-1"
                  >
                    {[4, 5, 6, 8, 10, 12, 15].map((v) => (
                      <option key={v} value={v}>
                        {v}s
                      </option>
                    ))}
                  </select>
                </label>
                <label className="flex items-center gap-2">
                  分辨率
                  <select
                    value={resolution}
                    onChange={(e) => setResolution(e.target.value as '768P' | '2K')}
                    className="rounded-lg border border-zinc-800 bg-zinc-900 px-2 py-1"
                  >
                    <option value="768P">768P</option>
                    <option value="2K">2K</option>
                  </select>
                </label>
                <label
                  className={`flex items-center gap-2 ${hasFirstLast || hasRef ? 'opacity-40' : ''}`}
                  title={
                    hasFirstLast || hasRef
                      ? '首尾帧/参考素材模式下画面比例由素材决定（adaptive）'
                      : undefined
                  }
                >
                  画面比例
                  <select
                    value={ratio}
                    onChange={(e) => setRatio(e.target.value as (typeof RATIO_VALUES)[number])}
                    disabled={hasFirstLast || hasRef}
                    className="rounded-lg border border-zinc-800 bg-zinc-900 px-2 py-1 disabled:cursor-not-allowed"
                  >
                    {RATIO_VALUES.map((r) => (
                      <option key={r} value={r}>
                        {r}
                      </option>
                    ))}
                  </select>
                </label>
              </div>

              {/* F6.5: 首尾帧 and 参考素材 are mutually exclusive — selecting
                  one greys out the other with an explanatory tooltip. */}
              <div
                className={hasRef ? 'opacity-40' : ''}
                title={hasRef ? '已选择参考素材，首尾帧模式不可用，点击移除参考素材以切换' : undefined}
              >
                <p className="mb-2 text-xs uppercase tracking-wide text-zinc-500">首尾帧</p>
                <div className="space-y-2">
                  <div>
                    <p className="mb-1 text-xs text-zinc-600">首帧</p>
                    <AssetPicker
                      type="image"
                      selected={firstFrameAssetId ? [firstFrameAssetId] : []}
                      onToggle={(id) => setFirstFrameAssetId((cur) => (cur === id ? '' : id))}
                      max={1}
                      disabled={hasRef}
                    />
                  </div>
                  <div>
                    <p className="mb-1 text-xs text-zinc-600">尾帧</p>
                    <AssetPicker
                      type="image"
                      selected={lastFrameAssetId ? [lastFrameAssetId] : []}
                      onToggle={(id) => setLastFrameAssetId((cur) => (cur === id ? '' : id))}
                      max={1}
                      disabled={hasRef}
                    />
                  </div>
                </div>
              </div>

              <div
                className={hasFirstLast ? 'opacity-40' : ''}
                title={hasFirstLast ? '已选择首尾帧，参考素材模式不可用，点击移除首尾帧以切换' : undefined}
              >
                <p className="mb-2 text-xs uppercase tracking-wide text-zinc-500">参考素材</p>
                <div className="space-y-2">
                  <div>
                    <p className="mb-1 text-xs text-zinc-600">参考图片</p>
                    <AssetPicker
                      type="image"
                      selected={refImageIds}
                      onToggle={(id) =>
                        setRefImageIds((cur) =>
                          cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id],
                        )
                      }
                      disabled={hasFirstLast}
                    />
                  </div>
                  <div>
                    <p className="mb-1 text-xs text-zinc-600">参考视频</p>
                    <AssetPicker
                      type="video"
                      selected={refVideoIds}
                      onToggle={(id) =>
                        setRefVideoIds((cur) =>
                          cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id],
                        )
                      }
                      disabled={hasFirstLast}
                    />
                  </div>
                </div>
              </div>

              <label className="flex items-center gap-2 text-sm text-zinc-400" title="生成前用 AI 深度理解并润色你的提示词，按用量额外计费">
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
            <div className="space-y-4">
              <div className="space-y-2">
                {vsShots.map((s, i) => (
                  <div key={i} className="flex gap-2">
                    <input
                      value={s}
                      onChange={(e) =>
                        setVsShots((cur) => cur.map((c, ci) => (ci === i ? e.target.value : c)))
                      }
                      className="flex-1 rounded-lg border border-zinc-800 bg-zinc-900 px-3 py-2 text-sm outline-none focus:border-violet-500"
                      placeholder={`第 ${i + 1} 段镜头描述`}
                    />
                    {vsShots.length > 1 && (
                      <button
                        onClick={() => setVsShots((cur) => cur.filter((_, ci) => ci !== i))}
                        className="rounded-lg border border-zinc-800 px-2 text-zinc-500 hover:text-red-400"
                      >
                        ×
                      </button>
                    )}
                  </div>
                ))}
                <button
                  onClick={() => setVsShots((cur) => [...cur, ''])}
                  className="text-sm text-violet-400 hover:text-violet-300"
                >
                  + 添加一段
                </button>
              </div>

              <div className="flex flex-wrap items-center gap-4 text-sm text-zinc-400">
                <label className="flex items-center gap-2">
                  每段时长
                  <select
                    value={vsDuration}
                    onChange={(e) => setVsDuration(Number(e.target.value))}
                    className="rounded-lg border border-zinc-800 bg-zinc-900 px-2 py-1"
                  >
                    {[4, 5, 6, 8, 10, 12, 15].map((v) => (
                      <option key={v} value={v}>
                        {v}s
                      </option>
                    ))}
                  </select>
                </label>
                <label className="flex items-center gap-2" title="无角色绑定时，锚点段落回退为纯文字生成，需要指定画面比例">
                  画面比例
                  <select
                    value={vsRatio}
                    onChange={(e) => setVsRatio(e.target.value as (typeof RATIO_VALUES)[number])}
                    className="rounded-lg border border-zinc-800 bg-zinc-900 px-2 py-1"
                  >
                    {RATIO_VALUES.map((r) => (
                      <option key={r} value={r}>
                        {r}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="flex items-center gap-2" title="每 N 段重新锚定一次角色参考图（r2va），其余段落用前一段尾帧续接（i2va）">
                  重新锚定间隔
                  <select
                    value={vsRecalibrateEvery}
                    onChange={(e) => setVsRecalibrateEvery(Number(e.target.value))}
                    className="rounded-lg border border-zinc-800 bg-zinc-900 px-2 py-1"
                  >
                    {[2, 3, 4, 5].map((v) => (
                      <option key={v} value={v}>
                        每 {v} 段
                      </option>
                    ))}
                  </select>
                </label>
              </div>
            </div>
          )}

          <div className="flex items-center gap-3">
            <button
              onClick={() => createJob.mutate()}
              disabled={
                createJob.isPending ||
                running ||
                insufficientBalance ||
                (tab === 'video.single' && !videoValidation.success)
              }
              className="rounded-lg bg-violet-500 px-5 py-2.5 font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
            >
              {createJob.isPending ? '提交中…' : `✦ ${estimate} 生成`}
            </button>
            {insufficientBalance && (
              <span className="text-sm text-red-400">积分不足（余额 {balance}）</span>
            )}
            {tab === 'video.single' && !videoValidation.success && (
              <span className="text-sm text-amber-400">
                {videoValidation.error.issues[0]?.message}
              </span>
            )}
          </div>

          {bizId && (
            <Link to={`/jobs/${bizId}`} className="text-sm text-violet-400 hover:text-violet-300">
              查看流程图 →
            </Link>
          )}

          <div className="mt-4 flex min-h-80 flex-wrap items-center justify-center gap-3 rounded-xl border border-zinc-800 bg-zinc-900 p-6">
            {!bizId && <p className="text-zinc-500">结果会显示在这里</p>}

            {gateSuspended && bizId && job && (
              <PreviewGate
                bizId={bizId}
                job={job}
                duration={vsDuration}
                onResumed={() => jobQuery.refetch()}
              />
            )}

            {running && !gateSuspended && (
              <GenerationProgress kind={tab.startsWith('video') ? 'video' : 'image'} />
            )}

            {job?.status === 'failed' && (
              <p className="text-red-400">
                生成失败：{displayNodeError(job && firstSpecificError(job.nodes))}
              </p>
            )}

            {job?.status === 'succeeded' &&
              assetIds.map((id) => (
                <div key={id} className="text-center">
                  <p className="mb-1 font-mono text-xs text-zinc-500">{id}</p>
                  <GeneratedMedia assetId={id} />
                </div>
              ))}
          </div>
        </main>
      </div>
    </div>
  )
}

function CharacterSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  options: { biz_id: string; name: string }[]
}) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <span className="w-4 text-zinc-500">{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="flex-1 rounded-lg border border-zinc-800 bg-zinc-900 px-2 py-1.5 text-zinc-300"
      >
        <option value="">未选择</option>
        {options.map((c) => (
          <option key={c.biz_id} value={c.biz_id}>
            {c.name}
          </option>
        ))}
      </select>
    </label>
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
    return (
      <video
        src={data.public_url}
        controls
        className="h-64 w-64 rounded-lg bg-black object-contain"
      />
    )
  }
  return <img src={data.public_url} alt="generated" className="h-64 w-64 rounded-lg object-cover" />
}
