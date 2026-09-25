import { useEffect, useRef, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { comicErrorText } from '../lib/comicErrorText'
import { failureText } from '../lib/errorText'
import { ComicError } from '../lib/comicError'
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
  const { t } = useTranslation('comic')
  const location = useLocation()
  const [doc, setDoc] = useState(() => {
    const spec = (location.state as { prefillComic?: Spec } | null)?.prefillComic
    if (!spec) return newComic(t('content.untitled'))
    return { ...newComic(t('content.untitled')), mode: spec.comic_mode ?? 'editable', brief: spec.text ?? '', context: spec.comic_context ?? '', references: (spec.reference_image_asset_ids ?? []).map(asset_id => ({ asset_id, label: t('content.referenceDefault') })) }
  })
  // D25: excerpts are sent only after the user confirms checking them; editing them resets that.
  const [checkedContext, setCheckedContext] = useState<string | null>(null)
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
      catch { setError(t('error.backupFull')) }
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
    try { await action() } catch (err) { setError(comicErrorText(t, err)) }
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
          if (typeof assetID !== 'string' || !assetID) throw new ComicError('noImage')
          // Delay completion handling during a save to keep version updates ordered.
          if (inFlight.current) { timer = setTimeout(poll, 1000); return }
          const next = applyGeneratedImage(live.current, assetID, pending.panel)
          change(next); setMessage(t('done.generated'))
          const output = job.nodes.find(n => n.name === 'compose')?.outputs
          if (output?.['usage-known'] === false) setError(t('error.usageUnknown'))
          return
        }
        if (['failed', 'cancelled'].includes(job.status)) {
          change({ ...live.current, pending: undefined }, false)
          const failed = job.nodes.find(n => n.error) as { error_code?: string } | undefined
          setError(failed?.error_code ? failureText(t, failed.error_code) : t('error.notFinished')); return
        }
      } catch (err) { if (!cancelled) setError(comicErrorText(t, err)) }
      if (!cancelled) timer = setTimeout(poll, 2500)
    }
    void poll()
    return () => { cancelled = true; clearTimeout(timer) }
  }, [doc.pending])

  function generationSpec(panel: number): Spec {
    const current = live.current
    const refs = current.references.map((r, i) => t('content.referenceLine', { n: i + 1, label: r.label || t('content.referenceUnnamed') })).join('\n')
    return { comic_mode: current.mode, comic_panel: panel, text: refs ? `${current.brief}\n\n${refs}` : current.brief, comic_context: current.context, image_provider: 'openai', reference_image_asset_ids: current.references.map(r => r.asset_id), source_image_asset_id: panel > 0 ? current.page_asset_id : undefined }
  }
  async function prepareGeneration() {
    if (!live.current.brief.trim()) throw new ComicError('briefRequired')
    if (charCount(live.current.brief) > 19000 || charCount(live.current.context) > 8000) throw new ComicError('tooLong')
    if (generationPanel > 0 && !live.current.page_asset_id) throw new ComicError('pageRequired')
    if (live.current.context.trim() && checkedContext !== live.current.context) throw new ComicError('checkRequired')
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
    setMessage(t('done.submitted'))
  }
  function toggleReference(assetID: string) {
    const refs = live.current.references
    if (refs.some(r => r.asset_id === assetID)) change({ ...live.current, references: refs.filter(r => r.asset_id !== assetID) })
    else if (refs.length < maxRefs) change({ ...live.current, references: [...refs, { asset_id: assetID, label: '' }] })
    else setError(t('error.tooManyReferences', { max: maxRefs }))
  }
  async function addImage(file: File | undefined, type: 'logo' | 'page') {
    if (!file) return
    if (!COMIC_IMAGE_MIMES.includes(file.type) || file.size > 20 * 1024 * 1024) throw new ComicError('imageFile')
    const asset = await uploadAsset(file)
    if (type === 'page') change(applyGeneratedImage(live.current, asset.biz_id, 0))
    else {
      const next = { ...newLayer('logo', crypto.randomUUID()), asset_id: asset.biz_id }
      change({ ...live.current, layers: [...live.current.layers, next] }); setSelected(next.id)
    }
  }
  return <AppShell><div className="comic-workspace">
    <header className="comic-header">
      <div><Link to="/create/comic-classic" className="text-sm text-zinc-400">{t('classic')}</Link><h1 className="mt-2 text-2xl font-semibold">{t('title')}</h1><p className="mt-1 text-sm text-zinc-400">{t('subtitle')}</p></div>
      <div className="comic-toolbar">
        <button disabled={busy} onClick={() => void run(async () => { await persist(live.current); setMessage(t('done.saved')) })}>{t('toolbar.save')}</button>
        <button disabled={busy} onClick={() => download(new Blob([JSON.stringify(live.current, null, 2)], { type: 'application/json' }), 'comic-editable.json')}>{t('toolbar.backup')}</button>
        <label className="comic-upload compact">{t('toolbar.import')}<input aria-label={t('toolbar.import')} type="file" accept="application/json,.json" disabled={busy || !!doc.pending} onChange={e => {
          const file = e.target.files?.[0]; e.target.value = ''
          if (file) void run(async () => {
            if (file.size > 2 * 1024 * 1024) throw new ComicError('draftFile')
            const imported = parseComicDocument(JSON.parse(await file.text()))
            if (!window.confirm(t('confirm.importDraft'))) return
            activate({ biz_id: '', version: 0, document: { ...imported, pending: undefined } })
            setMessage(t('done.imported'))
          })
        }} /></label>
        <button disabled={busy} onClick={() => void run(async () => {
          const previous = saved.current
          saved.current = { id: null, version: 0 }
          try { await persist(live.current); setMessage(t('done.copySaved')) } catch (err) { saved.current = previous; throw err }
        })}>{t('toolbar.saveCopy')}</button>
        <button className="primary" disabled={busy || !doc.page_asset_id} onClick={() => void run(async () => download(await exportComic(live.current, images, t('canvas.fontSample')), `${doc.title}.png`))}>{t('toolbar.export')}</button>
      </div>
    </header>
    {recovery && <div className="comic-notice">{t('recovery.text')}<button onClick={() => activate(recovery)}>{t('recovery.restore')}</button><button onClick={() => setRecovery(null)}>{t('recovery.ignore')}</button></div>}
    {error && <div role="alert" className="comic-notice error">{error}</div>}
    {message && <p role="status" className="comic-notice">{message}</p>}
    {drafts.isError && <p role="alert" className="comic-notice error">{t('draftsFailed')}</p>}
    <div className="comic-layout">
      <section className="comic-panel comic-inputs" aria-label={t('inputs.label')}>
        <label>{t('inputs.drafts')}<select value={savedID ?? ''} disabled={busy || !!doc.pending} onChange={event => { const id = event.target.value; if (id) void run(async () => { if ((live.current.brief || live.current.layers.length) && !window.confirm(t('confirm.switchDraft'))) return; activate(await api.getComic(id)) }) }}><option value="">{t('inputs.chooseDraft')}</option>{drafts.data?.comics.map(d => <option key={d.biz_id} value={d.biz_id}>{d.title}</option>)}</select></label>
        <button disabled={busy || !!doc.pending} onClick={() => { if (window.confirm(t('confirm.newComic'))) activate({ biz_id: '', version: 0, document: newComic(t('content.untitled')) }) }}>{t('inputs.newComic')}</button>
        <label>{t('inputs.title')}<input value={doc.title} maxLength={128} onChange={e => change({ ...doc, title: e.target.value })} /></label>
        <fieldset disabled={busy || !!doc.pending}><legend>{t('inputs.modeLegend')}</legend>
          <label className="comic-radio"><input type="radio" name="mode" checked={doc.mode === 'editable'} onChange={() => change({ ...doc, mode: 'editable' })} />{t('inputs.modeEditable')}</label>
          <label className="comic-radio"><input type="radio" name="mode" checked={doc.mode === 'direct'} onChange={() => { change({ ...doc, mode: 'direct' }); setGenerationPanel(0) }} />{t('inputs.modeDirect')}</label>
        </fieldset>
        <label>{t('inputs.brief')}<textarea rows={9} value={doc.brief} placeholder={t('inputs.briefPlaceholder')} onChange={e => change({ ...doc, brief: e.target.value })} /></label>
        <p className="comic-hint">{t('inputs.briefHint', { n: charCount(doc.brief).toLocaleString(), max: (19000).toLocaleString() })}</p>
        <details><summary>{t('inputs.sources')}</summary>
          <label className="comic-upload">{t('inputs.importSource')}<input aria-label={t('inputs.importSourceLabel')} type="file" accept=".pdf,.txt,.md" disabled={busy} onChange={e => { const file = e.target.files?.[0]; e.target.value = ''; if (file) void run(async () => { const text = await importComicSource(file, n => t('content.page', { n })); change({ ...live.current, background: text, context: '' }); setMessage(t('done.sourceImported')) }) }} /></label>
          <label>{t('inputs.background')}<textarea aria-label={t('inputs.background')} rows={5} value={doc.background} onChange={e => { if (charCount(e.target.value) <= 200000) change({ ...doc, background: e.target.value }); else setError(t('error.backgroundTooLong', { max: (200000).toLocaleString() })) }} /></label>
          <p className="comic-hint">{t('inputs.backgroundHint', { n: charCount(doc.background).toLocaleString(), max: (200000).toLocaleString() })}</p>
          <button disabled={!doc.background.trim()} onClick={() => change({ ...doc, context: sourceExcerpts(doc.background, doc.brief, (from, to) => t('content.range', { from, to })) })}>{t('inputs.extract')}</button>
        </details>
        <label>{t('inputs.context')}<textarea aria-label={t('inputs.context')} rows={4} value={doc.context} onChange={e => change({ ...doc, context: e.target.value })} /></label>
        <p className="comic-hint">{t('inputs.contextHint', { n: charCount(doc.context), max: (8000).toLocaleString() })}</p>
        {doc.context.trim() && <label className="comic-radio"><input type="checkbox" checked={checkedContext === doc.context} onChange={e => { setCheckedContext(e.target.checked ? doc.context : null); setQuote(null) }} />{t('inputs.checked')}</label>}
        <h2>{t('inputs.references', { n: doc.references.length, max: maxRefs })}</h2>
        <AssetPicker type="image" selected={doc.references.map(r => r.asset_id)} onToggle={toggleReference} max={maxRefs} disabled={busy || !!doc.pending}
          accept={COMIC_IMAGE_MIMES.join(',')} filter={a => COMIC_IMAGE_MIMES.includes(a.mime)} emptyHint={t('inputs.referencesEmpty')} />
        {doc.references.map((r, i) => <div className="comic-reference" key={r.asset_id}><ReferenceThumb assetID={r.asset_id} /><label>{t('inputs.referenceUse', { n: i + 1 })}<input value={r.label} maxLength={200} placeholder={t('inputs.referenceUsePlaceholder')} onChange={e => change({ ...doc, references: doc.references.map((v, n) => n === i ? { ...v, label: e.target.value } : v) })} /></label><button aria-label={t('inputs.removeReference', { n: i + 1 })} onClick={() => change({ ...doc, references: doc.references.filter((_, n) => n !== i) })}>{t('inputs.remove')}</button></div>)}
        <p className="comic-hint">{doc.references.length ? t('inputs.referencesCost') : t('inputs.referencesHint')}</p>
        <label>{t('inputs.target')}<select disabled={doc.mode === 'direct' || !!doc.pending} value={generationPanel} onChange={e => { setGenerationPanel(Number(e.target.value)); setQuote(null) }}><option value={0}>{t('inputs.targetPage')}</option>{[1, 2, 3, 4].map(i => <option key={i} value={i}>{t('inputs.targetPanel', { n: i })}</option>)}</select></label>
        <p className="comic-hint">{t('inputs.model', { model: capabilities.data?.comic?.model ?? 'GPT Image' })}</p>
        {me.data && !aiAllowed && <p className="comic-notice">{t('inputs.betaRequired')}</p>}
        {aiAllowed && capabilities.data && !capabilities.data.comic?.openai_enabled && <p className="comic-hint">{t('inputs.notConfigured')}</p>}
        {doc.pending ? <div role="status" className="comic-notice">{t('inputs.generating', { target: doc.pending.panel ? t('inputs.targetPanelN', { n: doc.pending.panel }) : t('inputs.targetWholePage') })}<Link to={`/jobs/${doc.pending.job_id}`}>{t('inputs.viewTask')}</Link></div> : <button className="primary" disabled={busy || !aiReady} onClick={() => void run(prepareGeneration)}>{t('inputs.reviewCost')}</button>}
        {quote && <div className="comic-notice"><p>{t('inputs.quote', { n: quote.credits })}</p><button className="primary" disabled={busy} onClick={() => void run(submitGeneration)}>{quote.panel ? t('inputs.confirmPanel', { n: quote.panel }) : t('inputs.confirmPage')}</button><button onClick={() => setQuote(null)}>{t('inputs.cancel')}</button></div>}
      </section>
      <section className="comic-art" aria-label={t('art.label')}>
        <div className="comic-panel">
          <div className="comic-toolbar mb-4">
            <button disabled={doc.layers.length >= 64} onClick={() => { const l = newLayer('bubble', crypto.randomUUID(), 0, t('content.bubbleText')); change({ ...doc, layers: [...doc.layers, l] }); setSelected(l.id) }}>{t('toolbar.addBubble')}</button>
            <button disabled={doc.layers.length >= 64} onClick={() => { const l = newLayer('text', crypto.randomUUID(), 0, t('content.bubbleText')); change({ ...doc, layers: [...doc.layers, l] }); setSelected(l.id) }}>{t('toolbar.addText')}</button>
            <label className="comic-upload compact">{t('toolbar.addLogo')}<input aria-label={t('toolbar.addLogo')} type="file" accept={COMIC_IMAGE_MIMES.join(',')} disabled={busy || doc.layers.length >= 64} onChange={e => { const file = e.target.files?.[0]; e.target.value = ''; void run(() => addImage(file, 'logo')) }} /></label>
            <label className="comic-upload compact">{t('toolbar.importPage')}<input aria-label={t('toolbar.importPage')} type="file" accept={COMIC_IMAGE_MIMES.join(',')} disabled={busy || !!doc.pending} onChange={e => { const file = e.target.files?.[0]; e.target.value = ''; void run(() => addImage(file, 'page')) }} /></label>
            <button disabled={!history.length || !!doc.pending} onClick={() => { const prior = history[history.length - 1]; setHistory(h => h.slice(0, -1)); setFuture(f => [...f, doc]); change({ ...prior, pending: undefined }, false) }}>{t('toolbar.undo')}</button>
            <button disabled={!future.length || !!doc.pending} onClick={() => { const next = future[future.length - 1]; setFuture(f => f.slice(0, -1)); setHistory(h => [...h, doc]); change({ ...next, pending: undefined }, false) }}>{t('toolbar.redo')}</button>
          </div>
          <ComicCanvas document={doc} selected={selected} onSelect={setSelected} onChange={updateLayer} onDelete={removeLayer} onImages={setImages} />
        </div>
        <div className="comic-inspectors">
          <section className="comic-panel"><h2>{t('art.layers')}</h2>{!doc.layers.length && <p className="comic-hint">{t('art.layersEmpty')}</p>}
            <div className="comic-layer-list">{doc.layers.map((l, i) => <button key={l.id} aria-pressed={selected === l.id} onClick={() => setSelected(l.id)}>{i + 1}. {l.kind === 'logo' ? t('art.logo') : l.text.slice(0, 24) || t('art.emptyText')}{l.locked ? t('art.locked') : ''}</button>)}</div>
          </section>
          <section className="comic-panel"><h2>{t('art.properties')}</h2>{layer ? <>
            {layer.kind !== 'logo' && <><label>{t('art.text')}<textarea aria-label={t('art.text')} rows={4} maxLength={2000} disabled={layer.locked} value={layer.text} onChange={e => updateLayer({ ...layer, text: e.target.value })} /></label><label>{t('art.fontSize')}<input type="number" min={12} max={96} disabled={layer.locked} value={layer.font_size} onChange={e => updateLayer({ ...layer, font_size: Math.max(12, Math.min(96, Number(e.target.value) || 12)) })} /></label></>}
            <div className="comic-properties">{(['x', 'y', 'w', 'h'] as const).map((key, i) => <label key={key}>{[t('art.left'), t('art.top'), t('art.width'), t('art.height')][i]}<input type="number" min={0} max={100} step={1} value={Math.round(layer[key] * 100)} disabled={layer.locked} onChange={e => updateLayer(constrainLayer({ ...layer, [key]: (Number(e.target.value) || 0) / 100 }))} /></label>)}</div>
            {layer.kind !== 'logo' && <div className="comic-properties"><label>{t('art.color')}<input type="color" disabled={layer.locked} value={layer.color} onChange={e => updateLayer({ ...layer, color: e.target.value })} /></label><label>{t('art.fill')}<input type="color" disabled={layer.locked} value={layer.fill} onChange={e => updateLayer({ ...layer, fill: e.target.value })} /></label></div>}
            {layer.kind === 'bubble' && <label>{t('art.tail')}<select disabled={layer.locked} value={layer.tail} onChange={e => updateLayer({ ...layer, tail: e.target.value as ComicLayer['tail'] })}><option value="left">{t('art.tailLeft')}</option><option value="right">{t('art.tailRight')}</option><option value="none">{t('art.tailNone')}</option></select></label>}
            <div className="comic-toolbar mt-3"><button onClick={() => updateLayer({ ...layer, locked: !layer.locked })}>{layer.locked ? t('art.unlock') : t('art.lock')}</button><button disabled={layer.locked} onClick={() => change({ ...doc, layers: [...doc.layers.filter(l => l.id !== layer.id), layer] })}>{t('art.toFront')}</button><button disabled={layer.locked || doc.layers.length >= 64} onClick={() => { const copy = constrainLayer({ ...layer, id: crypto.randomUUID(), x: layer.x + .02, y: layer.y + .02 }); change({ ...doc, layers: [...doc.layers, copy] }); setSelected(copy.id) }}>{t('art.duplicate')}</button><button disabled={layer.locked} onClick={() => removeLayer(layer.id)}>{t('art.delete')}</button></div>
          </> : <p className="comic-hint">{t('art.pick')}</p>}</section>
        </div>
        <p className="comic-hint">{t('art.footer')}</p>
      </section>
    </div>
  </div></AppShell>
}
