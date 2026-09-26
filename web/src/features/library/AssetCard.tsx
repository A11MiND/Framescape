import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Check, Music, Play } from 'lucide-react'
import { cn } from '../../ui'
import type { AssetInfo } from '../../lib/api/assets'
import { formatClipLength, formatDate } from '../../lib/format'
import { assetTitle } from './assetTitle'

interface Props {
  asset: AssetInfo
  projectName?: string
  selecting: boolean
  selected: boolean
  onToggle: () => void
  menu?: ReactNode
  footer?: ReactNode
}

/** One library item: its preview, kind and size, title and one line of context. */
export function AssetCard({ asset: a, projectName, selecting, selected, onToggle, menu, footer }: Props) {
  const { t, i18n } = useTranslation('library')
  const title = assetTitle(t, a)
  const size = a.type === 'video' || a.type === 'audio' ? (a.duration_ms ? formatClipLength(a.duration_ms) : '') : a.width && a.height ? `${a.width}×${a.height}` : ''
  const preview = (
    <span className="relative block aspect-[4/3] overflow-hidden rounded-t-card bg-surface-2">
      {a.type === 'audio' ? (
        <span className="flex size-full items-center justify-center text-fg-muted">
          <Music aria-hidden className="size-8" />
        </span>
      ) : a.thumb_url || a.type === 'image' ? (
        <img src={a.thumb_url || a.public_url} alt="" loading="lazy" className="size-full object-cover" />
      ) : (
        <video src={a.public_url} muted preload="metadata" className="size-full object-cover" />
      )}
      {a.type === 'video' && (
        <span aria-hidden className="absolute inset-0 flex items-center justify-center">
          <span className="inline-flex size-9 items-center justify-center rounded-full bg-black/55 text-white">
            <Play className="size-4" />
          </span>
        </span>
      )}
      <span className="absolute bottom-1.5 left-1.5 flex gap-1 text-badge font-medium text-white">
        <span className="rounded-badge bg-black/65 px-1.5 py-0.5">{t(`kind.${a.type === 'video' ? 'video' : a.type === 'audio' ? 'audio' : 'image'}`)}</span>
        {size && <span className="rounded-badge bg-black/65 px-1.5 py-0.5 tabular-nums">{size}</span>}
        {a.resolution_tag && <span className="rounded-badge bg-black/65 px-1.5 py-0.5">{a.resolution_tag}</span>}
      </span>
      {selecting && (
        <span
          aria-hidden
          className={cn(
            'absolute top-2 left-2 inline-flex size-6 items-center justify-center rounded-full border-2',
            selected ? 'border-primary bg-primary text-white' : 'border-white bg-black/30 text-transparent',
          )}
        >
          <Check className="size-4" />
        </span>
      )}
    </span>
  )
  return (
    <li className={cn('relative flex flex-col rounded-card border bg-surface', selected ? 'border-primary ring-1 ring-primary' : 'border-border')}>
      {selecting ? (
        <button type="button" aria-pressed={selected} aria-label={t('select.item', { name: title })} onClick={onToggle} className="block text-left">
          {preview}
        </button>
      ) : (
        <Link to={`/assets/${a.biz_id}`} aria-label={title} className="block">
          {preview}
        </Link>
      )}
      <div className="flex items-start gap-1 p-2.5">
        <div className="min-w-0 flex-1">
          <p className="truncate text-body font-medium text-fg">{title}</p>
          <p className="truncate text-caption text-fg-muted">
            {formatDate(a.created_at, i18n.language)} · {projectName || t('card.noProject')}
          </p>
          {footer}
        </div>
        {menu}
      </div>
    </li>
  )
}
