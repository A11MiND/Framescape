import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { Lock, TriangleAlert } from 'lucide-react'
import { Button, Card, Dialog, EmptyState, Field, SegmentedControl, Select, Skeleton } from '../../ui'
import { createApi, type CreateRequest, type OpenAICapabilities } from '../../lib/api/create'
import { projectsApi } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { hasOpenAIImage } from '../../lib/api/account'
import { formatNumber } from '../../lib/format'
import { useMe } from '../../app/useMe'
import { useCurrentProject } from '../../app/currentProject'
import { CreateNav } from './CreateNav'
import { PromptInput } from './PromptInput'
import { ReferencePicker } from './ReferencePicker'
import { CharacterPicker } from './CharacterPicker'
import { PresetPicker } from './PresetPicker'
import { CostBar } from './CostBar'
import { ResultPane } from './ResultPane'
import { useDraft } from './drafts'
import { useSubmit } from './useSubmit'
import { characterSlots, readPrefill } from './prefill'

type Mode = 'general' | 'direct'

interface GptDraft {
  mode: Mode
  text: string
  directText: string
  n: number
  size: string
  quality: string
  references: string[]
  directReferences: string[]
  characters: string[]
  presets: string[]
  project: string
  lastJob: string
}

const INITIAL: GptDraft = {
  mode: 'general', text: '', directText: '', n: 1, size: '', quality: '', references: [], directReferences: [],
  characters: [], presets: [], project: '', lastJob: '',
}

const DIRECT_MAX_CHARS = 20000

function gcd(a: number, b: number): number {
  return b ? gcd(b, a % b) : a
}

function useSizeLabel() {
  const { t } = useTranslation('create')
  return (size: string) => {
    const [w, h] = size.split('x').map(Number)
    if (!w || !h) return size
    const g = gcd(w, h)
    const shape = w === h ? 'square' : w > h ? 'landscape' : 'portrait'
    return `${t(`gpt.shape.${shape}`)} ${w / g}:${h / g}`
  }
}

/** A size choice drawn as its aspect ratio, so shapes compare at a glance. */
function SizeOption({ size, label }: { size: string; label: string }) {
  const [w, h] = size.split('x').map(Number)
  const scale = 14 / Math.max(w || 1, h || 1)
  return (
    <span className="inline-flex items-center gap-1.5">
      {w > 0 && h > 0 && <span aria-hidden className="rounded-[2px] border-[1.5px] border-current" style={{ width: Math.round(w * scale), height: Math.round(h * scale) }} />}
      <span className="truncate">{label}</span>
    </span>
  )
}

/** Keeps a stored option only while the deployment still offers it. */
function pickOption(stored: string, offered: string[], fallback: string) {
  if (stored && offered.includes(stored)) return { value: stored, gone: false }
  return { value: offered.includes(fallback) ? fallback : (offered[0] ?? ''), gone: Boolean(stored) }
}

function Access({ openai, entitled }: { openai?: OpenAICapabilities; entitled: boolean }) {
  const { t } = useTranslation('create')
  if (!openai?.enabled) {
    return <EmptyState icon={<TriangleAlert className="size-7" />} title={t('gpt.unavailable.title')} body={t('gpt.unavailable.body')} />
  }
  if (!entitled) return <EmptyState icon={<Lock className="size-7" />} title={t('gpt.notEnabled.title')} body={t('gpt.notEnabled.body')} />
  return null
}

function SentPreview({ request }: { request: CreateRequest }) {
  const { t } = useTranslation('create')
  const [open, setOpen] = useState(false)
  const body = JSON.stringify(request)
  const preview = useQuery({ queryKey: keys.preview(body), queryFn: ({ signal }) => createApi.preview(request, signal), enabled: open, retry: false })
  return (
    <div className="flex flex-col gap-2">
      <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="w-fit text-caption font-medium text-primary-text hover:underline">
        {open ? t('gpt.preview.hide') : t('gpt.preview.show')}
      </button>
      {open &&
        (preview.isPending ? (
          <p className="text-caption text-fg-muted">{t('gpt.preview.loading')}</p>
        ) : preview.isError ? (
          <p className="text-caption text-fg-muted">{t('gpt.preview.failed')}</p>
        ) : (
          <div className="flex flex-col gap-1 rounded-card bg-surface-2 p-3">
            <span className="text-caption text-fg-muted">{t('gpt.preview.prompt')}</span>
            <p className="max-h-48 overflow-y-auto text-caption whitespace-pre-wrap text-fg">{preview.data.prompt}</p>
            <span className="text-caption text-fg-muted">{t('gpt.preview.refs', { n: preview.data.references.length })}</span>
          </div>
        ))}
    </div>
  )
}

/** GPT Image (spec P23): general generation with characters and presets, or the prompt sent as written. */
export default function GptStudio() {
  const { t, i18n } = useTranslation('create')
  const location = useLocation()
  const navigate = useNavigate()
  const me = useMe()
  const [currentProject] = useCurrentProject(me.data?.biz_id)
  const [draft, update] = useDraft<GptDraft>(me.data?.biz_id, 'gpt', INITIAL)
  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities, staleTime: 60_000 })
  const openai = caps.data?.providers?.openai
  const entitled = hasOpenAIImage(me.data)
  const ready = Boolean(openai?.enabled && entitled)
  const characters = useQuery({ queryKey: keys.characters, queryFn: createApi.characters, enabled: ready })
  const presets = useQuery({ queryKey: keys.presets, queryFn: createApi.presets, enabled: ready })
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list, enabled: ready })
  const sizeLabel = useSizeLabel()
  const [confirming, setConfirming] = useState(false)
  const [gone, setGone] = useState<string | null>(null)

  useEffect(() => {
    const p = readPrefill(location.state, 'image.single')
    if (!p) return
    if (p.mode === 'direct') update({ mode: 'direct', directText: p.text ?? '', directReferences: p.references ?? [] })
    else update({ mode: 'general', text: p.text ?? '', n: p.n ?? 1, references: p.references ?? [], characters: p.characters ?? [], presets: p.presets ?? [], size: p.size ?? '', quality: p.quality ?? '' })
    navigate('.', { replace: true, state: {} })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state])

  // Options come only from the deployment; a stored value it no longer offers is replaced and repriced.
  const size = pickOption(draft.size, openai?.sizes ?? [], '1024x1024')
  const quality = pickOption(draft.quality, openai?.qualities ?? [], openai?.default_quality ?? 'high')
  useEffect(() => {
    if (!openai) return
    const changed = [size.gone && [draft.size, size.value], quality.gone && [draft.quality, quality.value]].filter(Boolean) as string[][]
    if (changed.length) {
      setGone(t('gpt.optionGone', { value: changed[0][0], next: changed[0][1] }))
      update({ size: size.value, quality: quality.value })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openai, size.gone, quality.gone])

  const maxN = Math.max(1, openai?.max_n ?? 1)
  const maxRefs = openai?.max_references ?? 16
  const charList = characters.data?.characters ?? []
  const charRefs = draft.characters.reduce((n, id) => n + (charList.find((c) => c.biz_id === id)?.ref_asset_ids.length ?? 0), 0)
  const direct = draft.mode === 'direct'
  const text = direct ? draft.directText : draft.text
  const maxChars = direct ? DIRECT_MAX_CHARS : (caps.data?.image.max_prompt_chars ?? 1500)
  const tooLong = [...text].length > maxChars
  const project = draft.project || currentProject || ''
  // An attached reference replaces the characters' reference images (their
  // descriptions still expand into the prompt); otherwise those are sent.
  const refCount = direct ? draft.directReferences.length : draft.references.length ? draft.references.length : charRefs
  const n = Math.min(draft.n, maxN)

  const request: CreateRequest | null =
    ready && text.trim() && !tooLong && refCount <= maxRefs
      ? direct
        ? {
            workflow_name: 'image.comic4',
            spec: { comic_mode: 'direct', image_provider: 'openai', text: draft.directText, ...(draft.directReferences.length ? { reference_image_asset_ids: draft.directReferences } : {}) },
            ...(project ? { project_id: project } : {}),
          }
        : {
            workflow_name: 'image.single',
            spec: {
              image_provider: 'openai',
              text: draft.text,
              image_size: size.value,
              image_quality: quality.value,
              ...(n > 1 ? { n } : {}),
              ...(draft.references[0] ? { source_image_asset_id: draft.references[0] } : {}),
              ...(draft.characters.length ? { characters: characterSlots(draft.characters) } : {}),
              ...(draft.presets.length ? { preset_ids: draft.presets } : {}),
            },
            ...(project ? { project_id: project } : {}),
          }
      : null
  const job = useSubmit(request, (bizId) => {
    setConfirming(false)
    update({ lastJob: bizId })
  })
  const credits = job.estimate?.data?.credits_total

  const confirmRows: [string, string][] = [
    [t('confirm.model'), openai?.image_model ?? ''],
    [t('confirm.mode'), t(`gpt.mode.${draft.mode}`)],
    [t('confirm.size'), direct ? '1536 × 1024' : sizeLabel(size.value)],
    [t('confirm.quality'), t(`gpt.qualityOpt.${direct ? 'high' : quality.value}`, { defaultValue: quality.value })],
    [t('confirm.count'), String(direct ? 1 : n)],
    [t('confirm.references'), String(refCount)],
    [t('confirm.reservation'), credits === undefined ? t('ui:unknownValue') : t('cost.value', { n: formatNumber(credits, i18n.language) })],
  ]

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6">
      <CreateNav mode="gpt" />
      <header className="flex flex-wrap items-center gap-3">
        <h1 className="text-title font-semibold text-fg">{t('gpt.title')}</h1>
        {openai?.image_model && <span className="rounded-full bg-primary-soft px-3 py-1 text-caption font-medium text-primary-text">{t('gpt.model', { model: openai.image_model })}</span>}
        <span className="rounded-full bg-surface-2 px-3 py-1 text-caption text-fg-muted">{t('gpt.beta')}</span>
      </header>
      {caps.isPending || me.isPending ? (
        <Skeleton className="h-64 w-full" />
      ) : !ready ? (
        <Card padding="none">
          <Access openai={openai} entitled={entitled} />
        </Card>
      ) : (
        <div className="grid items-start gap-6 xl:grid-cols-[400px_minmax(0,1fr)]">
          <Card padding="none" className="flex flex-col gap-5 p-4 lg:p-5">
            <div className="flex flex-col gap-1.5">
              <SegmentedControl<Mode>
                fullWidth
                label={t('gpt.mode.label')}
                value={draft.mode}
                onChange={(m) => update({ mode: m })}
                options={[
                  { value: 'general', label: t('gpt.mode.general') },
                  { value: 'direct', label: t('gpt.mode.direct') },
                ]}
              />
              <p className="text-caption text-fg-muted">{t(`gpt.modeHint.${draft.mode}`)}</p>
            </div>
            {gone && (
              <p role="status" className="rounded-card bg-warning-soft p-2.5 text-caption text-warning-fg">
                {gone}
              </p>
            )}
            <PromptInput
              key={draft.mode}
              label={t('prompt.label')}
              value={text}
              onChange={(v) => update(direct ? { directText: v } : { text: v })}
              maxChars={maxChars}
              canRewrite={!direct}
              characters={direct ? undefined : charList}
              onMentionCharacter={(c) => !draft.characters.includes(c.biz_id) && update({ characters: [...draft.characters, c.biz_id].slice(0, 6) })}
              error={tooLong ? t('prompt.tooLong', { max: maxChars }) : undefined}
              help={direct ? t('gpt.baked') : undefined}
            />
            <ReferencePicker
              label={t('reference.label')}
              value={direct ? draft.directReferences : draft.references}
              onChange={(ids) => update(direct ? { directReferences: ids } : { references: ids.slice(-1) })}
              limits={{ max: direct ? maxRefs : 1, formats: openai?.reference_formats, maxBytes: openai?.max_reference_bytes }}
            />
            {!direct && (
              <>
                <CharacterPicker characters={charList} value={draft.characters} onChange={(ids) => update({ characters: ids })} />
                <p className={refCount > maxRefs ? 'text-caption text-danger-fg' : 'text-caption text-fg-muted'}>{t('reference.total', { n: refCount, max: maxRefs })}</p>
                <PresetPicker presets={presets.data?.presets ?? []} value={draft.presets} onChange={(ids) => update({ presets: ids })} />
                <div className="flex flex-col gap-1.5">
                  <span className="text-label font-medium text-fg">{t('gpt.size')}</span>
                  <SegmentedControl
                    fullWidth
                    label={t('gpt.size')}
                    value={size.value}
                    onChange={(v) => update({ size: v })}
                    options={(openai?.sizes ?? []).map((s) => ({ value: s, label: <SizeOption size={s} label={sizeLabel(s)} /> }))}
                  />
                </div>
                <div className="grid gap-4 sm:grid-cols-2 [&>*]:min-w-0">
                  <Field label={t('gpt.quality')}>
                    <Select value={quality.value} onChange={(e) => update({ quality: e.target.value })}>
                      {(openai?.qualities ?? []).map((q) => (
                        <option key={q} value={q}>
                          {t(`gpt.qualityOpt.${q}`, { defaultValue: q })}
                        </option>
                      ))}
                    </Select>
                  </Field>
                  <div className="flex flex-col gap-1.5">
                    <span className="text-label font-medium text-fg">{t('count.label')}</span>
                    <SegmentedControl
                      fullWidth
                      label={t('count.label')}
                      value={String(n)}
                      onChange={(v) => update({ n: Number(v) })}
                      options={Array.from({ length: maxN }, (_, i) => ({ value: String(i + 1), label: String(i + 1) }))}
                    />
                  </div>
                </div>
                {request && <SentPreview request={request} />}
              </>
            )}
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
              credits={credits}
              calculating={Boolean(request) && job.stale}
              failed={Boolean(job.estimate?.isError)}
              usageBased
              notice={job.notice}
              actionLabel={t('action.reviewCost')}
              onAction={() => setConfirming(true)}
              loading={false}
              disabled={!job.canSubmit}
            />
          </Card>
          <ResultPane jobId={draft.lastJob || null} />
        </div>
      )}
      <Dialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('confirm.title')}
        locked={job.submitting}
        footer={
          <>
            <Button onClick={() => setConfirming(false)} disabled={job.submitting}>
              {t('ui:action.cancel')}
            </Button>
            <Button variant="primary" loading={job.submitting} disabled={!job.canSubmit && !job.submitting} onClick={job.submit}>
              {t('action.confirm')}
            </Button>
          </>
        }
      >
        <dl className="flex flex-col gap-2">
          {confirmRows.map(([k, v]) => (
            <div key={k} className="flex justify-between gap-3 text-body">
              <dt className="text-fg-muted">{k}</dt>
              <dd className="text-right font-medium text-fg tabular-nums">{v}</dd>
            </div>
          ))}
        </dl>
        <p className="mt-3 text-caption text-fg-muted">{t('cost.noteUsage')}</p>
        {job.notice && (
          <p role="alert" className="mt-2 text-caption text-warning-fg">
            {job.notice === 'priceChanged' ? t('error.priceChanged') : t('error.insufficient')}
          </p>
        )}
      </Dialog>
    </div>
  )
}
