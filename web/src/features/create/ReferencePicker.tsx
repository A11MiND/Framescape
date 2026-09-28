import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { Check, ImagePlus, Music, Upload, X } from 'lucide-react'
import { Button, Dialog, Skeleton, cn } from '../../ui'
import { createApi } from '../../lib/api/create'
import { assetsApi } from '../../lib/api/assets'
import { keys } from '../../lib/api/keys'
import { uploadAsset } from '../../lib/upload'
import { errorText } from '../../lib/errorText'
import { useToast } from '../../components/Toast'

export type ReferenceKind = 'image' | 'video' | 'audio'

const DEFAULT_ACCEPT: Record<ReferenceKind, string[]> = {
  image: ['image/png', 'image/jpeg', 'image/webp'],
  video: ['video/mp4', 'video/quicktime', 'video/webm'],
  audio: ['audio/mpeg', 'audio/wav', 'audio/x-wav', 'audio/mp4', 'audio/aac'],
}

const INDEX_LABEL: Record<ReferenceKind, string> = { image: 'prompt.imageN', video: 'reference.videoN', audio: 'reference.audioN' }
const PICK_TITLE: Record<ReferenceKind, string> = { image: 'reference.pickTitle', video: 'reference.pickTitleVideo', audio: 'reference.pickTitleAudio' }
const PICK_EMPTY: Record<ReferenceKind, string> = { image: 'reference.pickEmpty', video: 'reference.pickEmptyVideo', audio: 'reference.pickEmptyAudio' }

/** A still for any media kind: the image, a video's first frame, or an audio mark. */
function Media({ kind, url, thumb }: { kind: ReferenceKind; url: string; thumb?: string }) {
  if (kind === 'audio') {
    return (
      <span className="flex size-full items-center justify-center text-fg-muted">
        <Music aria-hidden className="size-6" />
      </span>
    )
  }
  if (kind === 'video' && !thumb) return <video src={url} muted preload="metadata" className="size-full object-cover" />
  return <img src={thumb || url} alt="" className="size-full object-cover" />
}

export interface ReferenceLimits {
  max: number
  formats?: string[]
  maxBytes?: number
}

function Thumb({ id, index, kind, onRemove }: { id: string; index: number; kind: ReferenceKind; onRemove: () => void }) {
  const { t } = useTranslation('create')
  const asset = useQuery({ queryKey: keys.assets.detail(id), queryFn: () => assetsApi.get(id) })
  return (
    <li className="relative size-20 overflow-hidden rounded-thumb border border-border bg-surface-2">
      {asset.data ? <Media kind={kind} url={asset.data.public_url} thumb={asset.data.thumb_url} /> : <Skeleton className="size-full" />}
      <span className="absolute bottom-1 left-1 rounded-badge bg-black/70 px-1.5 text-badge text-white">{t(INDEX_LABEL[kind], { n: index + 1 })}</span>
      <button
        type="button"
        onClick={onRemove}
        aria-label={t('reference.remove', { n: index + 1 })}
        className="absolute top-1 right-1 inline-flex size-6 items-center justify-center rounded-full bg-black/70 text-white hover:bg-black/85"
      >
        <X aria-hidden className="size-3.5" />
      </button>
    </li>
  )
}

/** Attached references: numbered thumbnails, library picker and upload, checked against the limits. */
export function ReferencePicker({
  value,
  onChange,
  limits,
  label,
  kind = 'image',
  required,
  onUploadSuccess,
}: {
  value: string[]
  onChange: (ids: string[]) => void
  limits: ReferenceLimits
  label: string
  kind?: ReferenceKind
  required?: boolean
  onUploadSuccess?: () => void
}) {
  const { t } = useTranslation('create')
  const toast = useToast()
  const input = useRef<HTMLInputElement>(null)
  const [picking, setPicking] = useState(false)
  const [chosen, setChosen] = useState<string[]>([])
  const [uploading, setUploading] = useState(false)
  const library = useQuery({ queryKey: ['assets', 'reference-picker', kind], queryFn: () => createApi.recentAssets(kind, 60), enabled: picking })
  const room = limits.max - value.length
  const fmtList = (limits.formats ?? DEFAULT_ACCEPT[kind]).map((f) => f.replace(/^[a-z]+\//, '').toUpperCase()).join(t('ui:listSeparator'))

  const allowed = (mime: string, size: number) => {
    if (!(limits.formats ?? DEFAULT_ACCEPT[kind]).includes(mime)) return t('reference.format', { formats: fmtList })
    if (limits.maxBytes && size > limits.maxBytes) return t('reference.tooLarge', { mb: Math.floor(limits.maxBytes / (1 << 20)) })
    return null
  }

  const upload = async (files: FileList | null) => {
    if (!files?.length) return
    const list = [...files].slice(0, Math.max(0, room))
    if (files.length > room) toast(t('reference.tooMany', { max: limits.max }))
    setUploading(true)
    const added: string[] = []
    for (const f of list) {
      const problem = allowed(f.type, f.size)
      if (problem) {
        toast(`${f.name}: ${problem}`)
        continue
      }
      try {
        added.push((await uploadAsset(f)).biz_id)
      } catch (err) {
        toast(`${f.name}: ${errorText(t, err)}`)
      }
    }
    setUploading(false)
    if (added.length) {
      onChange([...value, ...added])
      onUploadSuccess?.()
    }
  }

  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1.5 text-label font-medium text-fg">
        {label}
        {required ? (
          <span aria-hidden className="ml-0.5 text-danger-fg">
            *
          </span>
        ) : (
          <span className="ml-1 font-normal text-fg-muted">{t('ui:field.optional')}</span>
        )}
      </legend>
      <div className="flex flex-wrap items-start gap-2">
        {value.length > 0 && (
          <ul className="flex flex-wrap gap-2">
            {value.map((id, i) => (
              <Thumb key={id} id={id} index={i} kind={kind} onRemove={() => onChange(value.filter((x) => x !== id))} />
            ))}
          </ul>
        )}
        {room > 0 && (
          <>
            <button
              type="button"
              onClick={() => {
                setChosen([])
                setPicking(true)
              }}
              className="flex size-20 flex-col items-center justify-center gap-1 rounded-thumb border border-dashed border-border-control text-caption text-fg-muted hover:bg-surface-2 hover:text-fg"
            >
              <ImagePlus aria-hidden className="size-5" />
              {t('reference.fromLibrary')}
            </button>
            <button
              type="button"
              onClick={() => input.current?.click()}
              disabled={uploading}
              className="flex size-20 flex-col items-center justify-center gap-1 rounded-thumb border border-dashed border-border-control text-caption text-fg-muted hover:bg-surface-2 hover:text-fg disabled:opacity-60"
            >
              <Upload aria-hidden className="size-5" />
              {uploading ? t('reference.uploading') : t('reference.upload')}
            </button>
            <input
              ref={input}
              type="file"
              accept={(limits.formats ?? DEFAULT_ACCEPT[kind]).join(',')}
              multiple={limits.max > 1}
              className="sr-only"
              tabIndex={-1}
              aria-label={label}
              onChange={(e) => {
                upload(e.target.files)
                e.target.value = ''
              }}
            />
          </>
        )}
      </div>
      <Dialog
        open={picking}
        onOpenChange={setPicking}
        title={t(PICK_TITLE[kind])}
        description={t('reference.count', { n: value.length + chosen.length, max: limits.max })}
        size="form"
        footer={
          <>
            <Button onClick={() => setPicking(false)}>{t('ui:action.cancel')}</Button>
            <Button
              variant="primary"
              disabled={chosen.length === 0}
              onClick={() => {
                onChange([...value, ...chosen])
                setPicking(false)
              }}
            >
              {t('reference.confirm')}
            </Button>
          </>
        }
      >
        {library.isPending ? (
          <div className="grid grid-cols-3 gap-2 sm:grid-cols-4">
            {Array.from({ length: 8 }, (_, i) => (
              <Skeleton key={i} className="aspect-square" />
            ))}
          </div>
        ) : (library.data?.assets ?? []).length === 0 ? (
          <p className="py-8 text-center text-body text-fg-muted">{t(PICK_EMPTY[kind])}</p>
        ) : (
          <ul className="grid grid-cols-3 gap-2 sm:grid-cols-4">
            {library.data!.assets
              .filter((a) => !value.includes(a.biz_id))
              .map((a) => {
                const on = chosen.includes(a.biz_id)
                const problem = allowed(a.mime, 0)
                const full = !on && value.length + chosen.length >= limits.max
                return (
                  <li key={a.biz_id}>
                    <button
                      type="button"
                      aria-pressed={on}
                      disabled={Boolean(problem) || full}
                      title={problem ?? undefined}
                      onClick={() => setChosen((c) => (on ? c.filter((x) => x !== a.biz_id) : [...c, a.biz_id]))}
                      className={cn(
                        'relative block aspect-square w-full overflow-hidden rounded-thumb border-2 bg-surface-2 disabled:cursor-not-allowed disabled:opacity-40',
                        on ? 'border-primary' : 'border-transparent',
                      )}
                    >
                      <Media kind={kind} url={a.public_url} />
                      {on && (
                        <span className="absolute top-1.5 right-1.5 inline-flex size-6 items-center justify-center rounded-full bg-primary text-white">
                          <Check aria-hidden className="size-4" />
                        </span>
                      )}
                    </button>
                  </li>
                )
              })}
          </ul>
        )}
      </Dialog>
    </fieldset>
  )
}
