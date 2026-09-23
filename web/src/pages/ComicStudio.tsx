import { useEffect, useRef, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import AppShell from '../components/AppShell'
import { AssetPicker } from '../components/AssetPicker'
import ComicCanvas from '../components/ComicCanvas'
import { api, type Spec } from '../lib/api'
import { applyGeneratedImage, charCount, COMIC_IMAGE_MIMES, MAX_REFERENCES, constrainLayer, newComic, newLayer, parseComicDocument, sourceExcerpts, type ComicDocument, type ComicLayer, type SavedComic } from '../lib/comicDocument'
import { exportComic, type ComicImages } from '../lib/comicRender'
import { importComicSource } from '../lib/comicImport'
import { uploadAsset } from '../lib/upload'
import './ComicStudio.css'
import '@fontsource/noto-sans-tc/400.css'

function ReferenceThumb({ assetID }: { assetID: string }) {
  const asset = useQuery({ queryKey: ['asset', assetID], queryFn: () => api.getAsset(assetID), staleTime: Infinity })
  return asset.data ? <img src={asset.data.public_url} alt="" className="comic-reference-thumb" /> : <span className="comic-reference-thumb" />
}
function download(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob), a = document.createElement('a')
  a.href = url; a.download = name; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
}
export default function ComicStudio() {
  const location = useLocation()
  const [doc, setDoc] = useState(() => {
    const spec = (location.state as { prefillComic?: Spec } | null)?.prefillComic
    if (!spec) return newComic()
    return { ...newComic(), mode: spec.comic_mode ?? 'editable', brief: spec.text ?? '', context: spec.comic_context ?? '', references: (spec.reference_image_asset_ids ?? []).map(asset_id => ({ asset_id, label: '角色／画风要求见故事描述' })) }
  })
  const live = useRef(doc); live.current = doc
  const saved = useRef<{ id: string | null; version: number }>({ id: null, version: 0 })
  const [savedID, setSavedID] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [images, setImages] = useState<ComicImages>({})
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const inFlight = useRef(false)
  const [history, setHistory] = useState<ComicDocument[]>([])
  const [future, setFuture] = useState<ComicDocument[]>([])
  const [recovery, setRecovery] = useState<SavedComic | null>(null)
  const requestKey = useRef<{ payload: string; id: string } | null>(null)
  const [generationPanel, setGenerationPanel] = useState(0)
  const [quote, setQuote] = useState<{ spec: Spec; credits: number; panel: number } | null>(null)
  const me = useQuery({ queryKey: ['me'], queryFn: api.me })
  const capabilities = useQuery({ queryKey: ['capabilities'], queryFn: api.getCapabilities })
  const drafts = useQuery({ queryKey: ['comics'], queryFn: api.listComics })
  const layer = doc.layers.find(l => l.id === selected)
  const maxRefs = capabilities.data?.comic?.max_references ?? MAX_REFERENCES
  // Gray release: generation is per-account opt-in (GET /me's comic_ai);
  // everyone can still open the editor, import a base image and letter it.
  const aiAllowed = !!me.data?.comic_ai
  const aiReady = aiAllowed && !!capabilities.data?.comic?.openai_enabled
  const storageKey = me.data ? `aigc.comic-draft.${me.data.biz_id}` : null
  const storageKeyRef = useRef(storageKey); storageKeyRef.current = storageKey

  function backupLocal() {
    if (!storageKeyRef.current) return
    localStorage.setItem(storageKeyRef.current, JSON.stringify({ biz_id: saved.current.id ?? '', version: saved.current.version, document: live.current }))
  }

  useEffect(() => {
    if (!storageKey) return
    try { const raw = localStorage.getItem(storageKey); if (raw) { const data: SavedComic = JSON.parse(raw); setRecovery({ ...data, document: parseComicDocument(data.document) }) } } catch { /* server copy remains available */ }
  }, [storageKey])
  useEffect(() => {
    if (!storageKey || recovery) return
    const timer = setTimeout(() => {
      try { localStorage.setItem(storageKey, JSON.stringify({ biz_id: saved.current.id ?? '', version: saved.current.version, document: doc })) }
      catch { setError('浏览器备份空间不足，请点击「保存编辑稿」保存到服务器。') }
    }, 400)
    return () => clearTimeout(timer)
  }, [doc, storageKey, recovery, savedID])
  useEffect(() => {
    const handler = (event: BeforeUnloadEvent) => {
      try { backupLocal() } catch { /* the server copy is unaffected */ }
      if (live.current.brief || live.current.layers.length) { event.preventDefault(); event.returnValue = '' }
    }
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [])
  function change(next: ComicDocument, undoable = true) {
    if (undoable) { setHistory(h => [...h.slice(-39), live.current]); setFuture([]) }
    live.current = next; setDoc(next); setQuote(null)
  }
  function updateLayer(next: ComicLayer) { change({ ...live.current, layers: live.current.layers.map(l => l.id === next.id ? next : l) }) }
  function removeLayer(id: string) { change({ ...live.current, layers: live.current.layers.filter(l => l.id !== id || l.locked) }); setSelected(null) }
  async function persist(value: ComicDocument): Promise<SavedComic> {
    const result = await api.saveComic(saved.current.id, saved.current.version, value)
    saved.current = { id: result.biz_id, version: result.version }; setSavedID(result.biz_id)
    backupLocal()
    void drafts.refetch()
    return result
  }
  async function run(action: () => Promise<void>) {
    if (inFlight.current) return
    inFlight.current = true; setBusy(true); setError(''); setMessage('')
    try { await action() } catch (err) { setError(err instanceof Error ? err.message : '操作失败，请重试') }
    finally { inFlight.current = false; setBusy(false) }
  }
  function activate(value: SavedComic) {
    const parsed = parseComicDocument(value.document)
    saved.current = { id: value.biz_id || null, version: value.version }; setSavedID(value.biz_id || null)
    change(parsed, false); setHistory([]); setFuture([]); setSelected(null); setRecovery(null)
  }
  // Poll only one active generation. All edits are applied to the current document,
  // never the pre-request snapshot, so a completed render cannot erase new text.
  useEffect(() => {
    const pending = doc.pending
    if (!pending) return
    let cancelled = false, timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      try {
        const job = await api.getJob(pending.job_id)
        if (cancelled) return
        if (job.status === 'succeeded') {
          const assetID = job.nodes.find(n => n.name === 'compose')?.outputs?.['asset-id']
          if (typeof assetID !== 'string' || !assetID) throw new Error('生成完成但找不到图片，请打开任务详情检查。')
          // Delay completion handling during a save to keep version updates ordered.
          if (inFlight.current) { timer = setTimeout(poll, 1000); return }
          const next = applyGeneratedImage(live.current, assetID, pending.panel)
          change(next); setMessage('生成完成。对白和 Logo 图层已保留。请保存编辑稿。')
          const output = job.nodes.find(n => n.name === 'compose')?.outputs
          if (output?.['usage-known'] === false) setError('OpenAI 未返回用量，本次按预留额度结算，管理员可在 OpenAI 后台核对。')
          return
        }
        if (['failed', 'cancelled'].includes(job.status)) {
          change({ ...live.current, pending: undefined }, false)
          setError(job.nodes.find(n => n.error)?.error || '生成未完成，旧画面和对白仍保留。'); return
        }
      } catch (err) { if (!cancelled) setError(err instanceof Error ? err.message : '读取生成进度失败') }
      if (!cancelled) timer = setTimeout(poll, 2500)
    }
    void poll()
    return () => { cancelled = true; clearTimeout(timer) }
  }, [doc.pending])

  function generationSpec(panel: number): Spec {
    const current = live.current
    const refs = current.references.map((r, i) => `参考图 ${i + 1}：${r.label || '指定人物或画风'}。`).join('\n')
    return { comic_mode: current.mode, comic_panel: panel, text: refs ? `${current.brief}\n\n${refs}` : current.brief, comic_context: current.context, image_provider: 'openai', reference_image_asset_ids: current.references.map(r => r.asset_id), source_image_asset_id: panel > 0 ? current.page_asset_id : undefined }
  }
  async function prepareGeneration() {
    if (!live.current.brief.trim()) throw new Error('请先填写故事和四格画面要求。')
    if (charCount(live.current.brief) > 19000 || charCount(live.current.context) > 8000) throw new Error('故事要求或背景摘录超出限制。')
    if (generationPanel > 0 && !live.current.page_asset_id) throw new Error('请先生成或导入整页漫画。')
    const spec = generationSpec(generationPanel)
    const estimate = await api.estimateJob('image.comic4', spec)
    setQuote({ spec, credits: estimate.credits_total, panel: generationPanel })
  }
  async function submitGeneration() {
    if (!quote) return
    await persist(live.current)
    const payload = JSON.stringify(quote.spec)
    const pendingKey = storageKeyRef.current ? `${storageKeyRef.current}.submission` : null
    if (pendingKey && !requestKey.current) {
      try { const raw = localStorage.getItem(pendingKey); if (raw) { const attempt = JSON.parse(raw); if (attempt.payload === payload && typeof attempt.id === 'string') requestKey.current = attempt } } catch { /* use a fresh attempt if no valid recovery exists */ }
    }
    if (requestKey.current?.payload !== payload) requestKey.current = { payload, id: crypto.randomUUID() }
    if (pendingKey) localStorage.setItem(pendingKey, JSON.stringify(requestKey.current))
    const job = await api.createJob('image.comic4', quote.spec, requestKey.current.id)
    const next = { ...live.current, pending: { job_id: job.biz_id, panel: quote.panel } }
    change(next, false); requestKey.current = null
    if (pendingKey) localStorage.removeItem(pendingKey)
    await persist(next)
    setMessage('已提交，等待生成。可以继续编辑对白。')
  }
  function toggleReference(assetID: string) {
    const refs = live.current.references
    if (refs.some(r => r.asset_id === assetID)) change({ ...live.current, references: refs.filter(r => r.asset_id !== assetID) })
    else if (refs.length < maxRefs) change({ ...live.current, references: [...refs, { asset_id: assetID, label: '' }] })
    else setError(`最多添加 ${maxRefs} 张人物／画风参考图。`)
  }
  async function addImage(file: File | undefined, type: 'logo' | 'page') {
    if (!file) return
    if (!COMIC_IMAGE_MIMES.includes(file.type) || file.size > 20 * 1024 * 1024) throw new Error('图片请使用 20 MB 以内的 PNG、JPEG 或 WebP。')
    const asset = await uploadAsset(file)
    if (type === 'page') change(applyGeneratedImage(live.current, asset.biz_id, 0))
    else {
      const next = { ...newLayer('logo', crypto.randomUUID()), asset_id: asset.biz_id }
      change({ ...live.current, layers: [...live.current.layers, next] }); setSelected(next.id)
    }
  }
  return <AppShell><main className="comic-workspace">
    <header className="comic-header">
      <div><Link to="/" className="text-sm text-violet-400">返回创作台</Link><span className="mx-3 text-zinc-600">/</span><Link to="/" state={{ prefillJob: { workflowName: 'image.comic4', spec: {} } }} className="text-sm text-zinc-400">旧版 MiniMax／Gemini</Link><h1 className="mt-2 text-2xl font-semibold">四格漫画工作台</h1><p className="mt-1 text-sm text-zinc-400">先确定故事与人物，再生成画面。对白与 Logo 随时可改。</p></div>
      <div className="comic-toolbar">
        <button disabled={busy} onClick={() => void run(async () => { await persist(live.current); setMessage('编辑稿已保存到服务器。') })}>保存编辑稿</button>
        <button disabled={busy} onClick={() => download(new Blob([JSON.stringify(live.current, null, 2)], { type: 'application/json' }), 'comic-editable.json')}>备份编辑稿</button>
        <label className="comic-upload compact">导入编辑稿<input aria-label="导入编辑稿" type="file" accept="application/json,.json" disabled={busy || !!doc.pending} onChange={e => {
          const file = e.target.files?.[0]; e.target.value = ''
          if (file) void run(async () => {
            if (file.size > 2 * 1024 * 1024) throw new Error('编辑稿文件不能超过 2 MB')
            const imported = parseComicDocument(JSON.parse(await file.text()))
            if (!window.confirm('导入为新的编辑稿？当前内容请先保存。')) return
            activate({ biz_id: '', version: 0, document: { ...imported, pending: undefined } })
            setMessage('已导入编辑稿。素材必须仍在当前账号中；请点击保存。')
          })
        }} /></label>
        <button disabled={busy} onClick={() => void run(async () => {
          const previous = saved.current
          saved.current = { id: null, version: 0 }
          try { await persist(live.current); setMessage('已保存为独立副本。') } catch (err) { saved.current = previous; throw err }
        })}>另存副本</button>
        <button className="primary" disabled={busy || !doc.page_asset_id} onClick={() => void run(async () => download(await exportComic(live.current, images), `${doc.title}.png`))}>导出 PNG</button>
      </div>
    </header>
    {recovery && <div className="comic-notice">发现本机编辑备份。「恢复」会继续本机版本；服务器存在更新时会阻止覆盖。<button onClick={() => activate(recovery)}>恢复本机编辑</button><button onClick={() => setRecovery(null)}>忽略备份</button></div>}
    {error && <div role="alert" className="comic-notice error">{error}</div>}
    {message && <p role="status" className="comic-notice">{message}</p>}
    {drafts.isError && <p role="alert" className="comic-notice error">无法读取编辑稿。请确认服务已运行并已执行数据库升级。</p>}
    <div className="comic-layout">
      <section className="comic-panel comic-inputs" aria-label="故事与素材">
        <label>已保存的编辑稿<select value={savedID ?? ''} disabled={busy || !!doc.pending} onChange={event => { const id = event.target.value; if (id) void run(async () => { if ((live.current.brief || live.current.layers.length) && !window.confirm('切换编辑稿？请先保存当前修改。')) return; activate(await api.getComic(id)) }) }}><option value="">选择编辑稿</option>{drafts.data?.comics.map(d => <option key={d.biz_id} value={d.biz_id}>{d.title}</option>)}</select></label>
        <button disabled={busy || !!doc.pending} onClick={() => { if (window.confirm('新建漫画？当前内容请先保存。')) activate({ biz_id: '', version: 0, document: newComic() }) }}>新建四格漫画</button>
        <label>标题<input value={doc.title} maxLength={128} onChange={e => change({ ...doc, title: e.target.value })} /></label>
        <fieldset disabled={busy || !!doc.pending}><legend>生成方式</legend>
          <label className="comic-radio"><input type="radio" name="mode" checked={doc.mode === 'editable'} onChange={() => change({ ...doc, mode: 'editable' })} />可编辑漫画（无字底图）</label>
          <label className="comic-radio"><input type="radio" name="mode" checked={doc.mode === 'direct'} onChange={() => { change({ ...doc, mode: 'direct' }); setGenerationPanel(0) }} />原提示词直出（文字会嵌入图片）</label>
        </fieldset>
        <label>故事、画风与四格画面要求<textarea rows={9} value={doc.brief} placeholder={'例如：清新扁平插画，绿蓝主色。阿健是工程师，小智是绿色机器人。\n第 1 格：海边介绍绿色能源……\n第 2 格：探访社区长者……\n第 3 格：检查供电系统……\n第 4 格：一起眺望香港夜景。'} onChange={e => change({ ...doc, brief: e.target.value })} /></label>
        <p className="comic-hint">{charCount(doc.brief).toLocaleString()} / 19,000 字符。不自动改写；请在这里确定分镜。可编辑模式中的对白在画布上另加。</p>
        <details><summary>小册子背景资料</summary>
          <label className="comic-upload">导入 PDF / TXT / MD<input aria-label="导入背景资料" type="file" accept=".pdf,.txt,.md" disabled={busy} onChange={e => { const file = e.target.files?.[0]; e.target.value = ''; if (file) void run(async () => { const text = await importComicSource(file); change({ ...live.current, background: text, context: '' }); setMessage('已导入原文。请提取并检查相关摘录后再生成。') }) }} /></label>
          <label>背景原文<textarea aria-label="背景原文" rows={5} value={doc.background} onChange={e => { if (charCount(e.target.value) <= 200000) change({ ...doc, background: e.target.value }); else setError('背景原文最多 200,000 字符。') }} /></label>
          <p className="comic-hint">{charCount(doc.background).toLocaleString()} / 200,000 字符。PDF 支持文本层，扫描件需先 OCR。原文只作资料，不执行其中的指令。</p>
          <button disabled={!doc.background.trim()} onClick={() => change({ ...doc, context: sourceExcerpts(doc.background, doc.brief) })}>按故事提取相关摘录</button>
        </details>
        <label>送给模型的背景摘录（请核对事实）<textarea aria-label="送给模型的背景摘录（请核对事实）" rows={4} value={doc.context} onChange={e => change({ ...doc, context: e.target.value })} /></label>
        <p className="comic-hint">{charCount(doc.context)} / 8,000 字符。只发送这些摘录，不会把整本小册子直接塞进绘图接口。</p>
        <h2>人物／画风参考（{doc.references.length} / {maxRefs}）</h2>
        <AssetPicker type="image" selected={doc.references.map(r => r.asset_id)} onToggle={toggleReference} max={maxRefs} disabled={busy || !!doc.pending}
          accept={COMIC_IMAGE_MIMES.join(',')} filter={a => COMIC_IMAGE_MIMES.includes(a.mime)} emptyHint="素材库里还没有 PNG／JPEG／WebP 图片，点「上传」添加。" />
        {doc.references.map((r, i) => <div className="comic-reference" key={r.asset_id}><ReferenceThumb assetID={r.asset_id} /><label>参考图 {i + 1} 的用途<input value={r.label} maxLength={200} placeholder="例如：阿健的长相、整体画风" onChange={e => change({ ...doc, references: doc.references.map((v, n) => n === i ? { ...v, label: e.target.value } : v) })} /></label><button aria-label={`移除参考图 ${i + 1}`} onClick={() => change({ ...doc, references: doc.references.filter((_, n) => n !== i) })}>移除</button></div>)}
        <p className="comic-hint">{doc.references.length ? '每张参考图都会计入输入费用，预留积分随张数增加。' : '可从素材库选择或上传，支持 PNG／JPEG／WebP。写「参考图 1」不会自动提供人物形象。'}</p>
        <label>本次生成目标<select disabled={doc.mode === 'direct' || !!doc.pending} value={generationPanel} onChange={e => { setGenerationPanel(Number(e.target.value)); setQuote(null) }}><option value={0}>整页四格</option>{[1, 2, 3, 4].map(i => <option key={i} value={i}>只重画第 {i} 格</option>)}</select></label>
        <p className="comic-hint">OpenAI {capabilities.data?.comic?.model ?? 'GPT Image'} · 固定 high 品质、3:2、不透明 PNG。单格重画会把原整页作为额外风格参考。</p>
        {me.data && !aiAllowed && <p className="comic-notice">AI 生成目前为内测功能，你的账号尚未开通。仍可导入底图、编辑对白和导出；如需开通请联系管理员。</p>}
        {aiAllowed && capabilities.data && !capabilities.data.comic?.openai_enabled && <p className="comic-hint">管理员尚未配置 OpenAI；仍可导入图片并编辑对白。</p>}
        {doc.pending ? <div role="status" className="comic-notice">正在生成{doc.pending.panel ? `第 ${doc.pending.panel} 格` : '整页'}……<Link to={`/jobs/${doc.pending.job_id}`}>查看任务</Link></div> : <button className="primary" disabled={busy || !aiReady} onClick={() => void run(prepareGeneration)}>检查费用并生成</button>}
        {quote && <div className="comic-notice"><p>本次预留 {quote.credits} 积分。实际费用以返回用量结算；预估不是硬性费用上限。</p><button className="primary" disabled={busy} onClick={() => void run(submitGeneration)}>确认生成{quote.panel ? `第 ${quote.panel} 格` : '整页'}</button><button onClick={() => setQuote(null)}>取消</button></div>}
      </section>
      <section className="comic-art" aria-label="漫画编辑区">
        <div className="comic-panel">
          <div className="comic-toolbar mb-4">
            <button disabled={doc.layers.length >= 64} onClick={() => { const l = newLayer('bubble', crypto.randomUUID()); change({ ...doc, layers: [...doc.layers, l] }); setSelected(l.id) }}>添加对话框</button>
            <button disabled={doc.layers.length >= 64} onClick={() => { const l = newLayer('text', crypto.randomUUID()); change({ ...doc, layers: [...doc.layers, l] }); setSelected(l.id) }}>添加标题文字</button>
            <label className="comic-upload compact">添加 Logo<input aria-label="添加 Logo" type="file" accept={COMIC_IMAGE_MIMES.join(',')} disabled={busy || doc.layers.length >= 64} onChange={e => { const file = e.target.files?.[0]; e.target.value = ''; void run(() => addImage(file, 'logo')) }} /></label>
            <label className="comic-upload compact">导入底图<input aria-label="导入底图" type="file" accept={COMIC_IMAGE_MIMES.join(',')} disabled={busy || !!doc.pending} onChange={e => { const file = e.target.files?.[0]; e.target.value = ''; void run(() => addImage(file, 'page')) }} /></label>
            <button disabled={!history.length || !!doc.pending} onClick={() => { const prior = history[history.length - 1]; setHistory(h => h.slice(0, -1)); setFuture(f => [...f, doc]); change({ ...prior, pending: undefined }, false) }}>撤销</button>
            <button disabled={!future.length || !!doc.pending} onClick={() => { const next = future[future.length - 1]; setFuture(f => f.slice(0, -1)); setHistory(h => [...h, doc]); change({ ...next, pending: undefined }, false) }}>重做</button>
          </div>
          <ComicCanvas document={doc} selected={selected} onSelect={setSelected} onChange={updateLayer} onDelete={removeLayer} onImages={setImages} />
        </div>
        <div className="comic-inspectors">
          <section className="comic-panel"><h2>图层（从下到上）</h2>{!doc.layers.length && <p className="comic-hint">添加对话框后，就能在这里修改文字。Logo 直接叠加原文件，不让模型重画。</p>}
            <div className="comic-layer-list">{doc.layers.map((l, i) => <button key={l.id} aria-pressed={selected === l.id} onClick={() => setSelected(l.id)}>{i + 1}. {l.kind === 'logo' ? 'Logo' : l.text.slice(0, 24) || '空白文字'}{l.locked ? '（已锁定）' : ''}</button>)}</div>
          </section>
          <section className="comic-panel"><h2>图层属性</h2>{layer ? <>
            {layer.kind !== 'logo' && <><label>对白文字<textarea aria-label="对白文字" rows={4} maxLength={2000} disabled={layer.locked} value={layer.text} onChange={e => updateLayer({ ...layer, text: e.target.value })} /></label><label>字号<input type="number" min={12} max={96} disabled={layer.locked} value={layer.font_size} onChange={e => updateLayer({ ...layer, font_size: Math.max(12, Math.min(96, Number(e.target.value) || 12)) })} /></label></>}
            <div className="comic-properties">{(['x', 'y', 'w', 'h'] as const).map((key, i) => <label key={key}>{['左侧 %', '顶部 %', '宽度 %', '高度 %'][i]}<input type="number" min={0} max={100} step={1} value={Math.round(layer[key] * 100)} disabled={layer.locked} onChange={e => updateLayer(constrainLayer({ ...layer, [key]: (Number(e.target.value) || 0) / 100 }))} /></label>)}</div>
            {layer.kind !== 'logo' && <div className="comic-properties"><label>文字颜色<input type="color" disabled={layer.locked} value={layer.color} onChange={e => updateLayer({ ...layer, color: e.target.value })} /></label><label>气泡底色<input type="color" disabled={layer.locked} value={layer.fill} onChange={e => updateLayer({ ...layer, fill: e.target.value })} /></label></div>}
            {layer.kind === 'bubble' && <label>气泡尾巴<select disabled={layer.locked} value={layer.tail} onChange={e => updateLayer({ ...layer, tail: e.target.value as ComicLayer['tail'] })}><option value="left">左侧</option><option value="right">右侧</option><option value="none">无</option></select></label>}
            <div className="comic-toolbar mt-3"><button onClick={() => updateLayer({ ...layer, locked: !layer.locked })}>{layer.locked ? '解锁' : '锁定'}</button><button disabled={layer.locked} onClick={() => change({ ...doc, layers: [...doc.layers.filter(l => l.id !== layer.id), layer] })}>置顶</button><button disabled={layer.locked || doc.layers.length >= 64} onClick={() => { const copy = constrainLayer({ ...layer, id: crypto.randomUUID(), x: layer.x + .02, y: layer.y + .02 }); change({ ...doc, layers: [...doc.layers, copy] }); setSelected(copy.id) }}>复制</button><button disabled={layer.locked} onClick={() => removeLayer(layer.id)}>删除图层</button></div>
          </> : <p className="comic-hint">点击画布中的对话框，或从图层列表选择。</p>}</section>
        </div>
        <p className="comic-hint">浏览器会自动保留本机备份；「保存编辑稿」保存到账号。关闭前请保存。导出为 1536 × 1024 PNG，编辑稿另行保留。</p>
      </section>
    </div>
  </main></AppShell>
}
