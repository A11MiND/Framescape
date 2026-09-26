import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'
import { CircleCheck, CircleX, RotateCcw, UploadCloud, X } from 'lucide-react'
import { Button, DeterminateProgress, Drawer, IconButton, cn } from '../../ui'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { formatBytes } from '../../lib/format'
import { uploadAssetWithProgress } from '../../lib/upload'

type Kind = 'image' | 'video' | 'audio'

interface Item {
  id: string
  file: File
  progress: number
  status: 'uploading' | 'done' | 'failed'
  error?: string
  abort?: AbortController
}

const kindOf = (file: File): Kind | null => (file.type.startsWith('image/') ? 'image' : file.type.startsWith('video/') ? 'video' : file.type.startsWith('audio/') ? 'audio' : null)

/** Uploads straight to storage with real progress; failed files can be retried. */
export function UploadDrawer({ open, onOpenChange, limits }: { open: boolean; onOpenChange: (o: boolean) => void; limits: Record<Kind, number> }) {
  const { t, i18n } = useTranslation('library')
  const qc = useQueryClient()
  const input = useRef<HTMLInputElement>(null)
  const [items, setItems] = useState<Item[]>([])
  const [over, setOver] = useState(false)
  const size = (n: number) => formatBytes(n, i18n.language)
  const patch = (id: string, p: Partial<Item>) => setItems((cur) => cur.map((it) => (it.id === id ? { ...it, ...p } : it)))

  const start = (item: Item) => {
    const kind = kindOf(item.file)
    if (!kind) return patch(item.id, { status: 'failed', error: t('uploads.unsupported') })
    if (item.file.size > limits[kind]) return patch(item.id, { status: 'failed', error: t('uploads.tooLarge', { max: size(limits[kind]) }) })
    const abort = new AbortController()
    patch(item.id, { status: 'uploading', progress: 0, error: undefined, abort })
    uploadAssetWithProgress(item.file, (f) => patch(item.id, { progress: f }), abort.signal)
      .then(() => {
        patch(item.id, { status: 'done', progress: 1, abort: undefined })
        qc.invalidateQueries({ queryKey: keys.assets.all })
      })
      .catch((err) => {
        if (err instanceof DOMException && err.name === 'AbortError') setItems((cur) => cur.filter((x) => x.id !== item.id))
        else patch(item.id, { status: 'failed', error: errorText(t, err), abort: undefined })
      })
  }

  const add = (files: FileList | null) => {
    if (!files?.length) return
    const next = [...files].map((file) => ({ id: crypto.randomUUID(), file, progress: 0, status: 'uploading' as const }))
    setItems((cur) => [...next, ...cur])
    next.forEach(start)
  }

  return (
    <Drawer open={open} onOpenChange={onOpenChange} title={t('uploads.title')}>
      <div className="flex flex-col gap-4">
        <button
          type="button"
          onClick={() => input.current?.click()}
          onDragOver={(e) => {
            e.preventDefault()
            setOver(true)
          }}
          onDragLeave={() => setOver(false)}
          onDrop={(e) => {
            e.preventDefault()
            setOver(false)
            add(e.dataTransfer.files)
          }}
          className={cn(
            'flex flex-col items-center gap-2 rounded-card border-2 border-dashed px-4 py-8 text-center text-body',
            over ? 'border-primary bg-primary-soft text-primary-text' : 'border-border-control text-fg-muted hover:bg-surface-2',
          )}
        >
          <UploadCloud aria-hidden className="size-8" />
          {t('uploads.drop')}
          <span className="text-caption">{t('uploads.limits', { image: size(limits.image), video: size(limits.video), audio: size(limits.audio) })}</span>
        </button>
        <input
          ref={input}
          type="file"
          multiple
          accept="image/*,video/*,audio/*"
          aria-label={t('uploads.choose')}
          className="sr-only"
          tabIndex={-1}
          onChange={(e) => {
            add(e.target.files)
            e.target.value = ''
          }}
        />
        {items.length > 0 && (
          <section aria-label={t('uploads.queue')} className="flex flex-col gap-2">
            <div className="flex items-center justify-between">
              <h3 className="text-label font-semibold text-fg">{t('uploads.queue')}</h3>
              {items.some((i) => i.status === 'done') && (
                <Button size="sm" variant="ghost" onClick={() => setItems((cur) => cur.filter((i) => i.status !== 'done'))}>
                  {t('uploads.clearDone')}
                </Button>
              )}
            </div>
            <ul className="flex flex-col gap-2">
              {items.map((it) => (
                <li key={it.id} className="flex flex-col gap-1.5 rounded-card border border-border p-3">
                  <div className="flex items-center gap-2">
                    {it.status === 'done' ? (
                      <CircleCheck aria-hidden className="size-4 shrink-0 text-success" />
                    ) : it.status === 'failed' ? (
                      <CircleX aria-hidden className="size-4 shrink-0 text-danger-icon" />
                    ) : null}
                    <span className="min-w-0 flex-1 truncate text-body text-fg">{it.file.name}</span>
                    <span className="shrink-0 text-caption text-fg-muted tabular-nums">{size(it.file.size)}</span>
                    {it.status === 'uploading' && <IconButton size="sm" label={t('uploads.cancel')} icon={<X className="size-4" />} onClick={() => it.abort?.abort()} />}
                    {it.status === 'failed' && (
                      <>
                        {kindOf(it.file) && it.file.size <= (limits[kindOf(it.file)!] ?? 0) && (
                          <IconButton size="sm" label={t('uploads.retry')} icon={<RotateCcw className="size-4" />} onClick={() => start(it)} />
                        )}
                        <IconButton size="sm" label={t('uploads.remove')} icon={<X className="size-4" />} onClick={() => setItems((cur) => cur.filter((x) => x.id !== it.id))} />
                      </>
                    )}
                  </div>
                  {it.status === 'uploading' && (
                    <>
                      <DeterminateProgress value={it.progress * 100} label={it.file.name} />
                      <span className="text-caption text-fg-muted tabular-nums">{t('uploads.uploading', { pct: Math.round(it.progress * 100) })}</span>
                    </>
                  )}
                  {it.status === 'done' && <span className="text-caption text-success-fg">{t('uploads.done')}</span>}
                  {it.status === 'failed' && (
                    <span role="alert" className="text-caption text-danger-fg">
                      {t('uploads.failedReason', { reason: it.error })}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
    </Drawer>
  )
}
