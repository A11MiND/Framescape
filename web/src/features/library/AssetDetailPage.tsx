import { useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, ChevronDown, ChevronLeft, ChevronRight, Clapperboard, Copy, Download, Expand, ImagePlus, Minus, MoreHorizontal, Plus, RotateCcw, Trash2, UserRound } from 'lucide-react'
import { Button, Card, ConfirmDialog, EmptyState, Field, IconButton, Menu, Select, Skeleton, buttonClasses, cn } from '../../ui'
import { assetsApi } from '../../lib/api/assets'
import { createApi } from '../../lib/api/create'
import { jobsApi } from '../../lib/api/jobs'
import { projectsApi } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { ApiError } from '../../lib/api/client'
import { errorText } from '../../lib/errorText'
import { formatDateTime } from '../../lib/format'
import { uploadAsset } from '../../lib/upload'
import { useToast } from '../../components/Toast'
import { assetTitle } from './assetTitle'

const ZOOMS = [0.5, 0.75, 1, 1.5, 2, 3]

function formatName(mime: string) {
  return mime.split('/')[1]?.toUpperCase() ?? ''
}

/** A frame of a playing video as a PNG file, or null when the canvas is tainted. */
function captureFrame(video: HTMLVideoElement): Promise<File | null> {
  return new Promise((resolve) => {
    try {
      const canvas = document.createElement('canvas')
      canvas.width = video.videoWidth
      canvas.height = video.videoHeight
      canvas.getContext('2d')?.drawImage(video, 0, 0)
      canvas.toBlob((blob) => resolve(blob ? new File([blob], 'frame.png', { type: 'image/png' }) : null), 'image/png')
    } catch {
      resolve(null)
    }
  })
}

function ParamRows({ meta }: { meta: Record<string, unknown> }) {
  const rows = Object.entries(meta).filter(([k, v]) => k !== 'prompt' && (typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean') && String(v) !== '')
  return (
    <dl className="mt-2 flex flex-col gap-1">
      {rows.map(([k, v]) => (
        <div key={k} className="flex justify-between gap-3 text-caption">
          <dt className="text-fg-muted">{k}</dt>
          <dd className="text-right break-all text-fg">{String(v)}</dd>
        </div>
      ))}
    </dl>
  )
}

/** One of the user's assets (spec P13): the full media, its details and what to do with it. */
export default function AssetDetailPage() {
  const { t, i18n } = useTranslation('library')
  const { assetId = '' } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const toast = useToast()
  const video = useRef<HTMLVideoElement>(null)
  const viewer = useRef<HTMLDivElement>(null)
  const [zoom, setZoom] = useState(1)
  const [broken, setBroken] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [confirmPublish, setConfirmPublish] = useState(false)
  const asset = useQuery({ queryKey: keys.assets.detail(assetId), queryFn: () => assetsApi.get(assetId), retry: false })
  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities, staleTime: 60_000 })
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list })
  const neighbours = useQuery({ queryKey: [...keys.assets.all, 'list', {}], queryFn: () => assetsApi.list({}), select: (d) => d.assets.map((a) => a.biz_id) })
  const a = asset.data
  const job = useQuery({ queryKey: keys.jobs.detail(a?.job_biz_id ?? ''), queryFn: () => jobsApi.get(a!.job_biz_id!), enabled: Boolean(a?.job_biz_id) })
  const retention = caps.data?.assets?.trash_retention_days ?? 30
  const refresh = () => {
    qc.invalidateQueries({ queryKey: keys.assets.detail(assetId) })
    qc.invalidateQueries({ queryKey: keys.assets.all })
  }

  const remove = useMutation({
    mutationFn: () => assetsApi.batch('delete', [assetId]),
    onSuccess: () => {
      refresh()
      toast(t('done.deleted', { n: 1, count: 1 }), { label: t('done.undo'), onClick: () => void assetsApi.batch('restore', [assetId]).then(refresh) })
      navigate('/assets')
    },
    onError: (err) => toast(errorText(t, err)),
  })
  const publish = useMutation({
    mutationFn: (on: boolean) => assetsApi.setPublic(assetId, on),
    onSuccess: (_, on) => {
      setConfirmPublish(false)
      toast(on ? t('detail.publish.published') : t('detail.publish.unpublished'))
      refresh()
    },
    onError: (err) => toast(errorText(t, err)),
  })
  const project = useMutation({
    mutationFn: (id: string) => assetsApi.setProject(assetId, id),
    onSuccess: () => {
      toast(t('detail.projectSaved'))
      refresh()
    },
    onError: (err) => toast(errorText(t, err)),
  })
  const frame = useMutation({
    mutationFn: async () => {
      const v = video.current
      const file = v ? await captureFrame(v) : null
      if (file) return (await uploadAsset(file)).biz_id
      const first = job.data?.nodes.map((n) => (n.outputs as Record<string, unknown> | null)?.['first-frame-asset-id']).find((x): x is string => typeof x === 'string')
      if (!first) throw new Error('frame')
      toast(t('detail.frameFailed'))
      return first
    },
    onSuccess: (id) => navigate('/characters', { state: { prefillAssetId: id } }),
    onError: (err) => toast(err instanceof ApiError ? errorText(t, err) : t('detail.frameUnavailable')),
  })

  if (asset.isPending) {
    return (
      <div className="mx-auto grid max-w-[1440px] gap-5 px-4 py-6 lg:px-6 xl:grid-cols-[minmax(0,1fr)_400px]">
        <Skeleton className="aspect-[3/2] w-full" />
        <Skeleton className="h-96 w-full" />
      </div>
    )
  }
  if (!a || broken) {
    const notFound = asset.error instanceof ApiError && asset.error.status === 404
    return (
      <div className="mx-auto max-w-[720px] px-4 py-10">
        <Card padding="none">
          <EmptyState
            icon={<ImagePlus className="size-7" />}
            title={notFound ? t('detail.notFound.title') : t('detail.missing.title')}
            body={notFound ? t('detail.notFound.body') : t('detail.missing.body')}
            action={
              <Link to="/assets" className={buttonClasses('primary')}>
                {t('detail.missing.back')}
              </Link>
            }
          />
        </Card>
      </div>
    )
  }

  const title = assetTitle(t, a)
  const uploaded = a.source === 'upload' || !a.meta || !Object.keys(a.meta).length
  const at = neighbours.data?.indexOf(a.biz_id) ?? -1
  const prev = at > 0 ? neighbours.data![at - 1] : null
  const next = at >= 0 && at < (neighbours.data?.length ?? 0) - 1 ? neighbours.data![at + 1] : null
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(a.prompt)
      toast(t('detail.copied'))
    } catch {
      // the prompt stays selectable
    }
  }

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-4 px-4 py-6 lg:px-6">
      <Link to="/assets" className="inline-flex w-fit items-center gap-1.5 text-body text-fg-muted hover:text-fg">
        <ArrowLeft aria-hidden className="size-4" />
        {t('detail.back')}
      </Link>
      <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_400px]">
        <section className="flex min-w-0 flex-col gap-3">
          <div ref={viewer} className="relative flex aspect-[3/2] items-center justify-center overflow-auto rounded-card border border-border bg-surface-2">
            {a.type === 'video' ? (
              <video ref={video} src={a.public_url} poster={a.thumb_url || undefined} controls crossOrigin="anonymous" onError={() => setBroken(true)} className="max-h-full max-w-full bg-black" />
            ) : a.type === 'audio' ? (
              <audio src={a.public_url} controls onError={() => setBroken(true)} className="w-3/4" />
            ) : (
              <img
                src={a.public_url}
                alt={title}
                onError={() => setBroken(true)}
                style={{ transform: `scale(${zoom})` }}
                className="size-full origin-center object-contain transition-transform"
              />
            )}
            {a.resolution_tag && <span className="absolute top-3 left-3 rounded-badge bg-black/70 px-2 py-0.5 text-badge font-medium text-white">{a.resolution_tag}</span>}
            {prev && (
              <Link to={`/assets/${prev}`} aria-label={t('detail.prev')} className="absolute top-1/2 left-3 inline-flex size-9 -translate-y-1/2 items-center justify-center rounded-full bg-black/55 text-white hover:bg-black/70">
                <ChevronLeft aria-hidden className="size-5" />
              </Link>
            )}
            {next && (
              <Link to={`/assets/${next}`} aria-label={t('detail.next')} className="absolute top-1/2 right-3 inline-flex size-9 -translate-y-1/2 items-center justify-center rounded-full bg-black/55 text-white hover:bg-black/70">
                <ChevronRight aria-hidden className="size-5" />
              </Link>
            )}
          </div>
          {a.type === 'image' && (
            <div className="flex items-center gap-1">
              <IconButton size="sm" label={t('detail.zoomOut')} icon={<Minus className="size-4" />} disabled={zoom <= ZOOMS[0]} onClick={() => setZoom(ZOOMS[Math.max(0, ZOOMS.indexOf(zoom) - 1)])} />
              <button type="button" aria-label={t('detail.zoomReset')} onClick={() => setZoom(1)} className="h-8 min-w-14 rounded-[8px] px-2 text-caption text-fg tabular-nums hover:bg-surface-2">
                {Math.round(zoom * 100)}%
              </button>
              <IconButton size="sm" label={t('detail.zoomIn')} icon={<Plus className="size-4" />} disabled={zoom >= ZOOMS[ZOOMS.length - 1]} onClick={() => setZoom(ZOOMS[Math.min(ZOOMS.length - 1, ZOOMS.indexOf(zoom) + 1)])} />
              <IconButton size="sm" label={t('detail.fullscreen')} icon={<Expand className="size-4" />} onClick={() => void viewer.current?.requestFullscreen?.()} />
            </div>
          )}
        </section>

        <aside className="flex flex-col gap-4">
          <Card padding="md" className="flex flex-col gap-4">
            <div className="flex items-start gap-2">
              <h1 className="min-w-0 flex-1 text-section font-semibold break-words text-fg">{title}</h1>
              <Menu
                trigger={<IconButton label={t('detail.more')} icon={<MoreHorizontal className="size-5" />} />}
                items={[{ key: 'delete', label: t('detail.delete'), icon: <Trash2 className="size-4" />, danger: true, onSelect: () => setConfirmDelete(true) }]}
              />
            </div>
            <dl className="grid grid-cols-2 gap-3 text-body">
              <div>
                <dt className="text-caption text-fg-muted">{t('detail.size')}</dt>
                <dd className="text-fg tabular-nums">{[a.width && a.height ? `${a.width}×${a.height}` : '', formatName(a.mime)].filter(Boolean).join(' · ')}</dd>
              </div>
              <div>
                <dt className="text-caption text-fg-muted">{t('detail.created')}</dt>
                <dd className="text-fg">{formatDateTime(a.created_at, i18n.language)}</dd>
              </div>
            </dl>
            <div>
              <div className="flex items-center justify-between">
                <h2 className="text-caption text-fg-muted">{t('detail.prompt')}</h2>
                {a.prompt && <IconButton size="sm" label={t('detail.copy')} icon={<Copy className="size-4" />} onClick={copy} />}
              </div>
              <p className="text-body whitespace-pre-wrap text-fg">{a.prompt || t('detail.uploaded')}</p>
            </div>
            {!uploaded && a.meta && (
              <details className="group rounded-card border border-border">
                <summary className="flex cursor-pointer list-none items-center justify-between px-3 py-2 text-body text-fg">
                  {t('detail.params')}
                  <ChevronDown aria-hidden className="size-4 text-fg-muted transition-transform group-open:rotate-180" />
                </summary>
                <div className="border-t border-border px-3 pb-2">
                  <ParamRows meta={a.meta} />
                </div>
              </details>
            )}
            <div className="flex items-center justify-between gap-3 text-body">
              <span className="text-caption text-fg-muted">{t('detail.sourceTask')}</span>
              {a.job_biz_id ? (
                <Link to={`/jobs/${a.job_biz_id}`} className="text-primary-text hover:underline">
                  {job.data?.title || t('detail.openTask')}
                </Link>
              ) : (
                <span className="text-fg-muted">{t('detail.noTask')}</span>
              )}
            </div>
            <Field label={t('detail.project')}>
              <Select value={a.project_id} disabled={project.isPending} onChange={(e) => project.mutate(e.target.value)}>
                <option value="">{t('detail.noProject')}</option>
                {(projects.data?.projects ?? []).map((p) => (
                  <option key={p.biz_id} value={p.biz_id}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </Field>
          </Card>

          <div className="grid grid-cols-2 gap-2">
            <a href={a.public_url} download target="_blank" rel="noreferrer" className={cn(buttonClasses('primary', 'lg'), 'col-span-2')}>
              <Download aria-hidden className="size-5" />
              {t('detail.action.download')}
            </a>
            {a.type === 'image' && (
              <>
                <Button icon={<ImagePlus aria-hidden className="size-4" />} onClick={() => navigate('/create/image', { state: { prefillReferenceId: a.biz_id } })}>
                  {t('detail.action.createFrom')}
                </Button>
                <Button icon={<Clapperboard aria-hidden className="size-4" />} onClick={() => navigate('/create/video', { state: { prefillSuggestion: { kind: 'to-video', sourceAssetId: a.biz_id } } })}>
                  {t('detail.action.toVideo')}
                </Button>
                <Button className="col-span-2" icon={<UserRound aria-hidden className="size-4" />} onClick={() => navigate('/characters', { state: { prefillAssetId: a.biz_id } })}>
                  {t('detail.action.saveCharacter')}
                </Button>
              </>
            )}
            {a.type === 'video' && (
              <>
                <Button className="col-span-2" icon={<UserRound aria-hidden className="size-4" />} loading={frame.isPending} onClick={() => frame.mutate()}>
                  {t('detail.action.saveFrame')}
                </Button>
                {job.data && (
                  <Button className="col-span-2" icon={<RotateCcw aria-hidden className="size-4" />} onClick={() => navigate('/', { state: { prefillJob: { workflowName: job.data.workflow_name, spec: job.data.spec } } })}>
                    {t('detail.action.again')}
                  </Button>
                )}
              </>
            )}
          </div>

          <Card padding="md" className="flex flex-col gap-3">
            <div className="flex items-center justify-between gap-3">
              <h2 className="text-body font-semibold text-fg">{t('detail.publish.title')}</h2>
              <span className={cn('text-caption', a.is_public ? 'text-success-fg' : 'text-fg-muted')}>{a.is_public ? t('detail.publish.public') : t('detail.publish.private')}</span>
            </div>
            {a.is_public ? (
              <Button loading={publish.isPending} onClick={() => publish.mutate(false)}>
                {t('detail.publish.unpublish')}
              </Button>
            ) : (
              <Button variant="primary" onClick={() => setConfirmPublish(true)}>
                {t('detail.publish.publish')}
              </Button>
            )}
          </Card>
        </aside>
      </div>

      <ConfirmDialog
        open={confirmPublish}
        onOpenChange={setConfirmPublish}
        title={t('detail.publish.confirmTitle')}
        body={t('detail.publish.confirmBody')}
        effects={[t('detail.publish.what1'), t('detail.publish.what2'), t('detail.publish.notShared')]}
        confirmLabel={t('detail.publish.confirm')}
        busy={publish.isPending}
        onConfirm={() => publish.mutate(true)}
      />
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={t('detail.deleteTitle')}
        body={t('detail.deleteBody', { days: retention })}
        confirmLabel={t('detail.deleteConfirm')}
        danger
        busy={remove.isPending}
        onConfirm={() => remove.mutate()}
      />
    </div>
  )
}

