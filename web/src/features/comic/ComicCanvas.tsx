import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { assetsApi } from '../../lib/api/assets'
import { comicErrorText } from '../../lib/comicErrorText'
import { comicSize, constrainLayer, resizeImage, type ComicDocument, type ComicLayer } from '../../lib/comicDocument'
import { loadComicImage, renderComic, type ComicImages } from '../../lib/comicRender'
import { cn } from '../../ui'

interface Props {
  document: ComicDocument
  selected: string | null
  onSelect: (id: string) => void
  onChange: (layer: ComicLayer) => void
  onDelete: (id: string) => void
  onImages: (images: ComicImages) => void
  onPageSize: (assetId: string, width: number, height: number) => void
  onOverflow: (ids: string[]) => void
}

/** The page as it will be exported, with its lettering layers movable on top. */
export function ComicCanvas({ document: doc, selected, onSelect, onChange, onDelete, onImages, onPageSize, onOverflow }: Props) {
  const { t } = useTranslation('comic')
  const canvas = useRef<HTMLCanvasElement>(null)
  const surface = useRef<HTMLDivElement>(null)
  const cache = useRef<ComicImages>({})
  const [images, setImages] = useState<ComicImages>({})
  const [error, setError] = useState('')
  const [transient, setTransient] = useState<ComicLayer | null>(null)
  const drag = useRef<{ layer: ComicLayer; x: number; y: number; resize: boolean; next: ComicLayer } | null>(null)
  const size = comicSize(doc)
  const assetKey = JSON.stringify(Array.from(new Set([doc.page_asset_id, ...doc.panel_asset_ids, ...doc.layers.map((l) => l.asset_id ?? '')].filter(Boolean))))

  useEffect(() => {
    let cancelled = false
    const ids: string[] = JSON.parse(assetKey)
    Promise.all(
      ids.map(async (id) => {
        if (!cache.current[id]) cache.current[id] = await loadComicImage((await assetsApi.get(id)).public_url)
        return [id, cache.current[id]] as const
      }),
    )
      .then((entries) => {
        if (cancelled) return
        const next = Object.fromEntries(entries)
        setImages(next)
        onImages(next)
        setError('')
      })
      .catch((err) => !cancelled && setError(comicErrorText(t, err)))
    return () => {
      cancelled = true
    }
  }, [assetKey, onImages, t])

  useEffect(() => {
    const page = images[doc.page_asset_id]
    if (page) onPageSize(doc.page_asset_id, page.naturalWidth, page.naturalHeight)
  }, [images, doc.page_asset_id, doc.page_width, doc.page_height, onPageSize])

  useEffect(() => {
    let cancelled = false
    const rendered = transient ? { ...doc, layers: doc.layers.map((l) => (l.id === transient.id ? transient : l)) } : doc
    const text = rendered.layers.map((l) => l.text).join('')
    document.fonts
      .load('32px "Noto Sans TC"', text || t('canvas.fontSample'))
      .then(() => {
        if (!cancelled && canvas.current) onOverflow(renderComic(canvas.current, rendered, images, (n) => t('canvas.panel', { n })))
      })
      .catch(() => !cancelled && setError(t('error.fontFailed')))
    return () => {
      cancelled = true
    }
  }, [doc, images, transient, t, onOverflow])

  const stopDrag = (commit: boolean) => {
    // A click without movement adds no undo step.
    if (drag.current && commit && drag.current.next !== drag.current.layer) onChange(drag.current.next)
    drag.current = null
    setTransient(null)
  }

  return (
    <div className="flex flex-col gap-2">
      <div ref={surface} style={{ aspectRatio: `${size.width} / ${size.height}` }} className="relative w-full touch-none overflow-visible rounded-card border border-border bg-surface-2 select-none" aria-label={t('canvas.label')}>
        <canvas ref={canvas} role="img" aria-label={t('canvas.preview')} className="block size-full rounded-card" />
        {doc.layers
          .filter((l) => !l.hidden)
          .map((original) => {
            const l = constrainLayer(transient?.id === original.id ? transient : original, doc)
            const isSelected = selected === l.id
            return (
              <div
                key={l.id}
                role="button"
                tabIndex={0}
                data-layer={l.id}
                aria-label={l.kind === 'logo' ? t('canvas.layerLogo', { name: l.text || l.id }) : t('canvas.layerText', { name: l.text || l.id })}
                aria-pressed={isSelected}
                className={cn(
                  'absolute cursor-move rounded-[4px] outline-none',
                  isSelected ? 'ring-2 ring-primary ring-offset-1' : 'hover:ring-1 hover:ring-primary/60',
                  l.locked && 'cursor-default',
                  'focus-visible:ring-2 focus-visible:ring-primary',
                )}
                style={{ left: `${l.x * 100}%`, top: `${l.y * 100}%`, width: `${l.w * 100}%`, height: `${l.h * 100}%`, transform: l.kind === 'logo' ? `rotate(${l.rotation ?? 0}deg)` : undefined }}
                onFocus={() => onSelect(l.id)}
                onPointerDown={(event) => {
                  onSelect(l.id)
                  if (l.locked || event.button !== 0) return
                  event.preventDefault()
                  event.currentTarget.focus()
                  event.currentTarget.setPointerCapture(event.pointerId)
                  drag.current = { layer: l, x: event.clientX, y: event.clientY, resize: (event.target as HTMLElement).dataset.resize === 'true', next: l }
                }}
                onPointerMove={(event) => {
                  const d = drag.current
                  const bounds = surface.current?.getBoundingClientRect()
                  if (!d || !bounds) return
                  const dx = (event.clientX - d.x) / bounds.width
                  const dy = (event.clientY - d.y) / bounds.height
                  const angle = (d.layer.rotation ?? 0) * Math.PI / 180
                  const localX = dx * Math.cos(angle) + dy * size.height / size.width * Math.sin(angle)
                  const localY = -dx * size.width / size.height * Math.sin(angle) + dy * Math.cos(angle)
                  const proposed = d.resize && d.layer.kind === 'logo' ? resizeImage(d.layer, 'w', Math.max(.001, d.layer.w + localX)) : d.resize ? { ...d.layer, w: Math.min(1 - d.layer.x, d.layer.w + localX), h: Math.min(1 - d.layer.y, d.layer.h + localY) } : { ...d.layer, x: d.layer.x + dx, y: d.layer.y + dy }
                  const next = doc.auto_fit ? proposed : constrainLayer(proposed, doc)
                  d.next = next
                  setTransient(next)
                }}
                onPointerUp={() => stopDrag(true)}
                onPointerCancel={() => stopDrag(false)}
                onKeyDown={(event) => {
                  if (l.locked || event.nativeEvent.isComposing) return
                  if (event.key === 'Delete' || event.key === 'Backspace') {
                    event.preventDefault()
                    onDelete(l.id)
                    return
                  }
                  const delta = event.shiftKey ? 0.02 : 0.005
                  const vectors: Record<string, [number, number]> = { ArrowLeft: [-delta, 0], ArrowRight: [delta, 0], ArrowUp: [0, -delta], ArrowDown: [0, delta] }
                  if (vectors[event.key]) {
                    event.preventDefault()
                    const [dx, dy] = vectors[event.key]
                    onChange(doc.auto_fit ? { ...l, x: l.x + dx, y: l.y + dy } : constrainLayer({ ...l, x: l.x + dx, y: l.y + dy }, doc))
                  }
                }}
              >
                {isSelected && !l.locked && (
                  <span data-resize="true" aria-hidden className="absolute -right-1.5 -bottom-1.5 size-3.5 cursor-nwse-resize rounded-full border-2 border-surface bg-primary" />
                )}
              </div>
            )
          })}
      </div>
      {error && (
        <p role="alert" className="text-caption text-danger-fg">
          {error}
        </p>
      )}
      <p className="text-caption text-fg-muted">{t('canvas.hint')}</p>
    </div>
  )
}
