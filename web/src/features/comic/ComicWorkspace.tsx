import { useCallback, useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { MoreHorizontal, RotateCcw } from 'lucide-react'
import { Button, ConfirmDialog, Dialog, IconButton, Input, Menu, Select, cn } from '../../ui'
import { createApi, type CreateRequest } from '../../lib/api/create'
import { comicsApi } from '../../lib/api/comics'
import { hasOpenAIImage } from '../../lib/api/account'
import { keys } from '../../lib/api/keys'
import { ApiError } from '../../lib/api/client'
import { comicErrorText } from '../../lib/comicErrorText'
import { failureText } from '../../lib/errorText'
import { ComicError } from '../../lib/comicError'
import { formatClock, formatNumber } from '../../lib/format'
import { uploadAsset } from '../../lib/upload'
import { COMIC_IMAGE_MIMES, charCount, importPage, newComic, newLayer, parseComicDocument, type ComicDocument, type SavedComic } from '../../lib/comicDocument'
import { exportComic, type ComicImages } from '../../lib/comicRender'
import { useMe } from '../../app/useMe'
import { CreateNav } from '../create/CreateNav'
import { ComicCanvas } from './ComicCanvas'
import { Inspector } from './Inspector'
import { StoryPanel, type ComicLimits } from './StoryPanel'
import { ReferencePanel } from './ReferencePanel'
import { GeneratePanel, type AiState } from './GeneratePanel'
import { ExportPanel } from './ExportPanel'
import { Toolbar } from './Toolbar'
import { ComicConflict, useComicDraft } from './useComicDraft'
import { useComicGeneration } from './useComicGeneration'
import '@fontsource/noto-sans-tc/400.css'

const STEPS = ['story', 'art', 'text', 'export'] as const
type Step = (typeof STEPS)[number]
const isStep = (v: string | null): v is Step => (STEPS as readonly string[]).includes(v ?? '')

type Pending = { kind: 'new' } | { kind: 'switch'; id: string } | { kind: 'import'; doc: ComicDocument } | { kind: 'delete' }

function download(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/**
 * The GPT editable comic (spec P05/P06): story and references, the page
 * drawn by GPT Image, lettering on top, and the PNG export, as four steps
 * around one canvas.
 */
export default function ComicWorkspace() {
  const { t, i18n } = useTranslation('comic')
  const location = useLocation()
  const navigate = useNavigate()
  const { draftId } = useParams()
  const [params, setParams] = useSearchParams()
  const me = useMe()
  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities, staleTime: 60_000 })
  const drafts = useQuery({ queryKey: keys.comics.all, queryFn: () => comicsApi.list() })
  const userId = me.data?.biz_id
  const draft = useComicDraft(userId, () => {
    const spec = (location.state as { prefillComic?: Record<string, unknown> } | null)?.prefillComic
    const base = newComic(t('content.untitled'))
    if (!spec) return base
    return {
      ...base,
      mode: spec.comic_mode === 'direct' ? 'direct' : 'editable',
      brief: typeof spec.text === 'string' ? spec.text : '',
      context: typeof spec.comic_context === 'string' ? spec.comic_context : '',
      references: ((spec.reference_image_asset_ids as string[] | undefined) ?? []).map((asset_id) => ({ asset_id, label: t('content.referenceDefault') })),
    }
  })
  const doc = draft.doc
  const step: Step = isStep(params.get('step')) ? (params.get('step') as Step) : draftId ? 'text' : 'story'
  const setStep = (s: Step) => {
    const next = new URLSearchParams(params)
    next.set('step', s)
    setParams(next, { replace: true })
  }

  const [selected, setSelected] = useState<string | null>(null)
  const [inspectorTab, setInspectorTab] = useState<'layers' | 'properties'>('layers')
  const [images, setImages] = useState<ComicImages>({})
  const [overflow, setOverflow] = useState<string[]>([])
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const inFlight = useRef(false)
  const [checkedContext, setCheckedContext] = useState<string | null>(null)
  const [scope, setScope] = useState(0)
  const [quote, setQuote] = useState<{ request: CreateRequest; credits: number; panel: number } | null>(null)
  const [conflict, setConflict] = useState<SavedComic | null>(null)
  const [pendingAction, setPendingAction] = useState<Pending | null>(null)
  const importInput = useRef<HTMLInputElement>(null)

  const comic = caps.data?.comic
  const openai = caps.data?.providers?.openai
  const limits: ComicLimits = {
    brief: comic?.max_brief_chars ?? 20000,
    context: comic?.max_context_chars ?? 8000,
    background: comic?.max_background_chars ?? 200000,
    references: comic?.max_references ?? 15,
    referenceLabel: comic?.max_reference_label_chars ?? 200,
  }
  const maxLayers = comic?.max_layers ?? 64
  const ai: AiState = !me.data || !caps.data ? 'loading' : !hasOpenAIImage(me.data) ? 'beta' : !comic?.openai_enabled ? 'unconfigured' : 'ready'
  const storageKey = userId ? `aigc.comic-draft.${userId}` : null

  const run = useCallback(async (action: () => Promise<void>, fallback = 'generic') => {
    if (inFlight.current) return
    inFlight.current = true
    setBusy(true)
    setError('')
    setMessage('')
    try {
      await action()
    } catch (err) {
      if (err instanceof ComicConflict && draft.savedId) {
        try {
          setConflict(await comicsApi.get(draft.savedId))
        } catch (e) {
          setError(comicErrorText(t, e))
        }
      } else setError(comicErrorText(t, err, fallback))
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }, [draft.savedId, t])

  // Opening /create/comic/:id/edit loads that draft once.
  useEffect(() => {
    if (!draftId || draft.savedId === draftId) return
    void run(async () => draft.activate(await comicsApi.get(draftId)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draftId])

  const generation = useComicGeneration(draft, storageKey, {
    onDone: (usageUnknown) => {
      setMessage(t('done.generated'))
      if (usageUnknown) setError(t('error.usageUnknown'))
    },
    onFailed: (code) => setError(code ? failureText(t, code) : t('error.notFinished')),
    onError: (err) => setError(comicErrorText(t, err)),
  })

  const change = (next: ComicDocument, undoable = true) => {
    draft.change(next, undoable)
    setQuote(null)
  }
  const removeLayer = (id: string) => {
    change({ ...draft.live.current, layers: draft.live.current.layers.filter((l) => l.id !== id || l.locked) })
    setSelected(null)
  }
  // Layers are edited in the inspector, which the dialogue and export steps show.
  const selectLayer = (id: string) => {
    setSelected(id)
    setInspectorTab('properties')
    if (step === 'story' || step === 'art') setStep('text')
  }
  const addLayer = (kind: 'bubble' | 'text') => {
    const l = newLayer(kind, crypto.randomUUID(), 0, t('content.bubbleText'))
    change({ ...draft.live.current, layers: [...draft.live.current.layers, l] })
    selectLayer(l.id)
  }
  const addImage = (file: File, as: 'logo' | 'page') =>
    run(async () => {
      if (!COMIC_IMAGE_MIMES.includes(file.type) || file.size > 20 * 1024 * 1024) throw new ComicError('imageFile')
      const asset = await uploadAsset(file)
      if (as === 'page') {
        change(importPage(draft.live.current, asset.biz_id))
        setMessage(t('done.pageImported'))
      } else {
        const l = { ...newLayer('logo', crypto.randomUUID()), asset_id: asset.biz_id }
        change({ ...draft.live.current, layers: [...draft.live.current.layers, l] })
        selectLayer(l.id)
      }
    })

  const generationRequest = (panel: number): CreateRequest => {
    const current = draft.live.current
    const refs = current.references.map((r, i) => t('content.referenceLine', { n: i + 1, label: r.label || t('content.referenceUnnamed') })).join('\n')
    return {
      workflow_name: 'image.comic4',
      spec: {
        comic_mode: current.mode,
        comic_panel: panel,
        text: refs ? `${current.brief}\n\n${refs}` : current.brief,
        comic_context: current.context,
        image_provider: 'openai',
        reference_image_asset_ids: current.references.map((r) => r.asset_id),
        ...(panel > 0 ? { source_image_asset_id: current.page_asset_id } : {}),
      },
    }
  }
  const review = (panel: number) =>
    run(async () => {
      const current = draft.live.current
      if (!current.brief.trim()) throw new ComicError('briefRequired')
      const request = generationRequest(panel)
      if (charCount(String(request.spec.text)) > (comic?.max_composed_chars ?? 20000) || charCount(current.context) > limits.context) throw new ComicError('tooLong')
      if (panel > 0 && !current.page_asset_id) throw new ComicError('pageRequired')
      if (current.context.trim() && checkedContext !== current.context) throw new ComicError('checkRequired')
      const estimate = await generation.quote(request)
      setQuote({ request, credits: estimate.credits_total, panel })
    })

  const exportPng = () =>
    run(async () => {
      try {
        download(await exportComic(draft.live.current, images, t('canvas.fontSample')), `${draft.live.current.title}.png`)
      } catch (err) {
        if (err instanceof ComicError && err.code === 'overflow') setStep('export')
        throw err
      }
    })

  const perform = (action: Pending | null) => {
    setPendingAction(null)
    if (!action) return
    if (action.kind === 'new') draft.activate({ biz_id: '', version: 0, document: newComic(t('content.untitled')) })
    if (action.kind === 'switch') void run(async () => draft.activate(await comicsApi.get(action.id)))
    if (action.kind === 'import') {
      draft.activate({ biz_id: '', version: 0, document: { ...action.doc, pending: undefined } })
      setMessage(t('done.imported'))
    }
    if (action.kind === 'delete' && draft.savedId) {
      const id = draft.savedId
      void run(async () => {
        await comicsApi.remove(id)
        draft.activate({ biz_id: '', version: 0, document: newComic(t('content.untitled')) })
        void drafts.refetch()
        setMessage(t('done.deleted'))
        if (draftId) navigate('/create/comic', { replace: true })
      })
    }
  }
  const hasWork = Boolean(doc.brief.trim() || doc.layers.length || doc.page_asset_id)
  // Replacing the document asks first only when there is unsaved work to lose.
  const guard = (action: Pending) => (hasWork && draft.dirty ? setPendingAction(action) : perform(action))

  const saveStatus = draft.dirty
    ? draft.backedUpAt
      ? t('status.backedUp', { time: formatClock(draft.backedUpAt, i18n.language) })
      : t('status.unsaved')
    : draft.savedAt
      ? t('status.saved', { time: formatClock(draft.savedAt, i18n.language) })
      : draft.savedId
        ? t('status.savedEarlier')
        : t('status.unsaved')
  const pending = Boolean(doc.pending)
  const canRedrawPanels = doc.mode === 'editable' && Boolean(doc.page_asset_id) && ai === 'ready' && !pending

  const inspector = (
    <Inspector
      doc={doc}
      selected={selected}
      tab={inspectorTab}
      onTab={setInspectorTab}
      onSelect={selectLayer}
      onLayer={draft.updateLayer}
      onDocument={(d) => change(d)}
      onRemove={removeLayer}
      overflow={overflow}
    />
  )
  const references = (
    <ReferencePanel onUploadSuccess={() => { setError(''); setMessage(t('done.referencesUploaded')) }} doc={doc} limits={limits} formats={openai?.reference_formats ?? COMIC_IMAGE_MIMES} maxBytes={openai?.max_reference_bytes} onChange={(d) => change(d)} disabled={busy || pending} />
  )
  const left =
    step === 'story' ? (
      <StoryPanel
        doc={doc}
        limits={limits}
        checked={checkedContext === doc.context}
        onCheck={(c) => {
          setCheckedContext(c ? doc.context : null)
          setQuote(null)
        }}
        onChange={(d) => change(d)}
        run={(a) => void run(a)}
        onMessage={setMessage}
        onError={setError}
        disabled={busy}
      />
    ) : step === 'art' ? (
      <GeneratePanel
        doc={doc}
        scope={doc.mode === 'direct' ? 0 : scope}
        onScope={(p) => {
          setScope(p)
          setQuote(null)
        }}
        onMode={(m) => {
          change({ ...doc, mode: m })
          if (m === 'direct') setScope(0)
        }}
        model={comic?.model ?? ''}
        ai={ai}
        busy={busy}
        onReview={() => void review(doc.mode === 'direct' ? 0 : scope)}
      />
    ) : step === 'export' ? (
      <ExportPanel
        doc={doc}
        overflow={overflow}
        busy={busy}
        onFix={(id) => {
          selectLayer(id)
          document.querySelector<HTMLElement>(`[data-layer="${id}"]`)?.focus()
        }}
        onExport={() => void exportPng()}
        onBackup={() => download(new Blob([JSON.stringify(draft.live.current, null, 2)], { type: 'application/json' }), 'comic-editable.json')}
      />
    ) : null

  return (
    <div className="mx-auto flex max-w-[1600px] flex-col gap-4 px-4 py-6 lg:px-6">
      <CreateNav mode="comic" />
      <header className="flex flex-col gap-3 rounded-card border border-border bg-surface p-3 lg:p-4">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <Input aria-label={t('inputs.title')} value={doc.title} maxLength={comic?.max_title_chars ?? 128} onChange={(e) => change({ ...doc, title: e.target.value })} className="h-10 max-w-80 text-section font-semibold" />
          <span className="rounded-badge bg-surface-2 px-2 py-0.5 text-caption text-fg-muted">{doc.mode === 'editable' ? t('header.metaEditable') : t('header.metaDirect')}</span>
          {comic?.model && <span className="rounded-full bg-primary-soft px-2.5 py-0.5 text-caption font-medium text-primary-text">{t('header.model', { model: comic.model })}</span>}
          <span role="status" aria-live="polite" className={cn('text-caption', draft.backupFailed ? 'text-warning-fg' : 'text-fg-muted')}>
            {draft.backupFailed ? t('error.backupFull') : saveStatus}
          </span>
          <div className="ml-auto flex items-center gap-2">
            <Button disabled={busy} onClick={() => void run(async () => {
              await draft.persist()
              setMessage(t('done.saved'))
            }, 'saveFailed')}>
              {t('toolbar.save')}
            </Button>
            <Button variant="primary" disabled={busy || !doc.page_asset_id} onClick={() => void exportPng()}>
              {t('toolbar.export')}
            </Button>
            <Menu
              trigger={<IconButton label={t('header.more')} icon={<MoreHorizontal className="size-5" />} />}
              items={[
                { key: 'new', label: t('inputs.newComic'), onSelect: () => guard({ kind: 'new' }), disabled: busy || pending },
                { key: 'copy', label: t('toolbar.saveCopy'), onSelect: () => void run(async () => {
                  await draft.saveCopy()
                  setMessage(t('done.copySaved'))
                }, 'saveFailed'), disabled: busy },
                { key: 'backup', label: t('toolbar.backup'), onSelect: () => download(new Blob([JSON.stringify(draft.live.current, null, 2)], { type: 'application/json' }), 'comic-editable.json') },
                { key: 'import', label: t('toolbar.import'), onSelect: () => importInput.current?.click(), disabled: busy || pending },
                { key: 'delete', label: t('header.delete'), danger: true, onSelect: () => setPendingAction({ kind: 'delete' }), disabled: busy || !draft.savedId },
              ]}
            />
            <input
              ref={importInput}
              type="file"
              accept="application/json,.json"
              aria-label={t('toolbar.import')}
              className="sr-only"
              tabIndex={-1}
              onChange={(e) => {
                const file = e.target.files?.[0]
                e.target.value = ''
                if (file)
                  void run(async () => {
                    if (file.size > 2 * 1024 * 1024) throw new ComicError('draftFile')
                    let value: unknown
                    try { value = JSON.parse(await file.text()) } catch { throw new ComicError('invalidDraft') }
                    guard({ kind: 'import', doc: parseComicDocument(value) })
                  })
              }}
            />
          </div>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <nav aria-label={t('steps.label')} className="w-full sm:w-auto">
            <ol className="grid grid-cols-4 gap-1 sm:flex sm:flex-wrap sm:items-center">
              {STEPS.map((s, i) => (
                <li key={s} className="flex min-w-0 items-center gap-1">
                  {i > 0 && <span aria-hidden className="hidden text-fg-muted sm:inline">→</span>}
                  <button
                    type="button"
                    aria-current={step === s ? 'step' : undefined}
                    onClick={() => setStep(s)}
                    className={cn('inline-flex h-9 w-full min-w-0 items-center justify-center gap-1 rounded-full px-1.5 text-caption font-medium sm:w-auto sm:gap-2 sm:px-3 sm:text-body', step === s ? 'bg-primary-soft text-primary-text' : 'text-fg-muted hover:bg-surface-2 hover:text-fg')}
                  >
                    <span className={cn('inline-flex size-5 items-center justify-center rounded-full text-badge tabular-nums', step === s ? 'bg-primary text-white' : 'bg-surface-2')}>{i + 1}</span>
                    {t(`steps.${s}`)}
                  </button>
                </li>
              ))}
            </ol>
          </nav>
          <label className="flex items-center gap-2 text-caption text-fg-muted">
            {t('inputs.drafts')}
            <Select
              value={draft.savedId ?? ''}
              disabled={busy || pending}
              onChange={(e) => {
                const id = e.target.value
                if (!id) return
                guard({ kind: 'switch', id })
              }}
              className="h-9 max-w-64"
            >
              <option value="">{t('inputs.chooseDraft')}</option>
              {(drafts.data?.comics ?? []).map((d) => (
                <option key={d.biz_id} value={d.biz_id}>
                  {d.title}
                </option>
              ))}
            </Select>
          </label>
        </div>
      </header>

      {draft.recovery && (
        <div role="status" className="flex flex-wrap items-center gap-3 rounded-card border border-warning bg-warning-soft p-3 text-body text-warning-fg">
          <span className="flex-1">{t('recovery.text')}</span>
          <Button size="sm" onClick={() => draft.activate(draft.recovery!)}>
            {t('recovery.restore')}
          </Button>
          <Button size="sm" variant="ghost" onClick={draft.dismissRecovery}>
            {t('recovery.ignore')}
          </Button>
        </div>
      )}
      {error && (
        <div role="alert" className="rounded-card border border-danger bg-danger-soft p-3 text-body text-danger-fg">
          {error}
        </div>
      )}
      {message && (
        <p role="status" className="rounded-card bg-surface-2 p-3 text-body text-fg">
          {message}
        </p>
      )}
      {drafts.isError && <p role="alert" className="text-caption text-danger-fg">{t('draftsFailed')}</p>}

      <div
        className={cn(
          'grid items-start gap-4',
          step === 'text' ? 'xl:grid-cols-[72px_minmax(0,1fr)_320px]' : 'xl:grid-cols-[320px_minmax(0,1fr)_320px]',
        )}
      >
        <aside aria-label={t(`steps.${step}`)} className={cn('order-2 xl:order-1', step === 'text' ? 'hidden xl:block' : 'rounded-card border border-border bg-surface p-4')}>
          {step === 'text' ? (
            <Toolbar vertical canAdd={doc.layers.length < maxLayers} canUndo={draft.canUndo && !pending} canRedo={draft.canRedo && !pending} busy={busy} pending={pending} onAdd={addLayer} onLogo={(f) => void addImage(f, 'logo')} onPage={(f) => void addImage(f, 'page')} onUndo={draft.undo} onRedo={draft.redo} />
          ) : (
            left
          )}
        </aside>
        <section aria-label={t('art.label')} className="order-1 flex min-w-0 flex-col gap-3 xl:order-2">
          <div className={cn(step === 'text' && 'xl:hidden')}>
            <Toolbar vertical={false} canAdd={doc.layers.length < maxLayers} canUndo={draft.canUndo && !pending} canRedo={draft.canRedo && !pending} busy={busy} pending={pending} onAdd={addLayer} onLogo={(f) => void addImage(f, 'logo')} onPage={(f) => void addImage(f, 'page')} onUndo={draft.undo} onRedo={draft.redo} />
          </div>
          <ComicCanvas document={doc} selected={selected} onSelect={selectLayer} onChange={draft.updateLayer} onDelete={removeLayer} onImages={setImages} onOverflow={setOverflow} />
          {overflow.length > 0 && (
            <p role="alert" className="text-caption text-warning-fg">
              {t('canvas.overflow', { n: overflow.length })}
            </p>
          )}
          {canRedrawPanels && (
            <div className="flex flex-wrap items-center gap-2">
              {[1, 2, 3, 4].map((n) => (
                <Button key={n} size="sm" icon={<RotateCcw aria-hidden className="size-4" />} onClick={() => {
                  setScope(n)
                  setStep('art')
                  void review(n)
                }}>
                  {t('canvas.redraw', { n })}
                </Button>
              ))}
            </div>
          )}
        </section>
        <aside aria-label={step === 'story' || step === 'art' ? t('inputs.referencesTitle') : t('inspector.label')} className="order-3 rounded-card border border-border bg-surface p-4">
          {step === 'story' || step === 'art' ? references : inspector}
        </aside>
      </div>

      <Dialog
        open={quote !== null}
        onOpenChange={(o) => !o && setQuote(null)}
        title={t('generate.confirmTitle')}
        locked={busy}
        footer={
          <>
            <Button onClick={() => setQuote(null)} disabled={busy}>
              {t('inputs.cancel')}
            </Button>
            <Button
              variant="primary"
              loading={busy}
              onClick={() =>
                quote &&
                void run(async () => {
                  try {
                    await generation.submit(quote.request, quote.credits, quote.panel)
                    setQuote(null)
                    setMessage(t('done.submitted'))
                  } catch (err) {
                    if (err instanceof ApiError && err.code === 'price_changed') {
                      setQuote(null)
                      await review(quote.panel)
                    }
                    throw err
                  }
                })
              }
            >
              {quote?.panel ? t('inputs.confirmPanel', { n: quote.panel }) : t('inputs.confirmPage')}
            </Button>
          </>
        }
      >
        {quote && (
          <dl className="flex flex-col gap-2 text-body">
            <div className="flex justify-between gap-3">
              <dt className="text-fg-muted">{t('generate.confirmModel')}</dt>
              <dd className="text-right font-medium text-fg">{comic?.model}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-fg-muted">{t('generate.confirmScope')}</dt>
              <dd className="text-right font-medium text-fg">{quote.panel ? t('inputs.targetPanel', { n: quote.panel }) : t('inputs.targetPage')}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-fg-muted">{t('generate.confirmReferences')}</dt>
              <dd className="text-right font-medium text-fg tabular-nums">{doc.references.length}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-fg-muted">{t('generate.confirmReserve')}</dt>
              <dd className="text-right font-semibold text-fg tabular-nums">{t('generate.credits', { n: formatNumber(quote.credits, i18n.language) })}</dd>
            </div>
            <p className="mt-2 text-caption text-fg-muted">{t('inputs.quote', { n: formatNumber(quote.credits, i18n.language) })}</p>
          </dl>
        )}
      </Dialog>

      <Dialog
        open={conflict !== null}
        onOpenChange={(o) => !o && setConflict(null)}
        title={t('conflict.title')}
        description={t('conflict.body')}
        locked={busy}
        footer={
          <div className="flex w-full flex-col gap-2 sm:flex-row sm:justify-end">
            <Button onClick={() => {
              const server = conflict
              setConflict(null)
              if (server) {
                draft.activate(server, true)
                setMessage(t('conflict.loaded'))
              }
            }}>
              {t('conflict.load')}
            </Button>
            <Button onClick={() => {
              setConflict(null)
              void run(async () => {
                await draft.saveCopy()
                setMessage(t('done.copySaved'))
              })
            }}>
              {t('conflict.copy')}
            </Button>
            <Button variant="primary" onClick={() => {
              setConflict(null)
              void run(async () => {
                await draft.overwrite()
                setMessage(t('done.saved'))
              })
            }}>
              {t('conflict.keep')}
            </Button>
          </div>
        }
      >
        <ul className="list-disc pl-5 text-body text-fg">
          <li>{t('conflict.keepHelp')}</li>
          <li>{t('conflict.loadHelp')}</li>
          <li>{t('conflict.copyHelp')}</li>
        </ul>
      </Dialog>

      <ConfirmDialog
        open={pendingAction !== null}
        onOpenChange={(o) => !o && setPendingAction(null)}
        title={pendingAction ? t(`confirmDialog.${pendingAction.kind}.title`) : ''}
        body={pendingAction ? t(`confirmDialog.${pendingAction.kind}.body`) : ''}
        confirmLabel={pendingAction ? t(`confirmDialog.${pendingAction.kind}.confirm`) : ''}
        danger={pendingAction?.kind === 'delete'}
        onConfirm={() => perform(pendingAction)}
      />
    </div>
  )
}
