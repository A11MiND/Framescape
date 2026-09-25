import { useId, type DragEvent, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, Copy, GripVertical, ImageIcon, MoreHorizontal, Trash2 } from 'lucide-react'
import { IconButton, Menu, Skeleton, StatusPill, Textarea, cn } from '../../ui'
import { assetsApi } from '../../lib/api/assets'
import { keys } from '../../lib/api/keys'
import { failureText } from '../../lib/errorText'
import { stepPillStatus } from '../tasks/detail/model'
import { effectiveRef, type SequenceMode, type SequenceShot } from './sequence'
import type { ShotResult } from './shotResults'

function ResultImage({ assetId, label }: { assetId: string; label: string }) {
  const asset = useQuery({ queryKey: keys.assets.detail(assetId), queryFn: () => assetsApi.get(assetId) })
  if (!asset.data) return <Skeleton className="size-full" />
  return <img src={asset.data.thumb_url || asset.data.public_url} alt={label} className="size-full object-cover" />
}

interface Props {
  shots: SequenceShot[]
  index: number
  mode: SequenceMode
  maxChars: number
  result?: ShotResult
  disabled?: boolean
  canAdd: boolean
  dragging: boolean
  onText: (text: string) => void
  onRef: (ref: string | null) => void
  onMove: (to: number) => void
  onDuplicate: () => void
  onRemove: () => void
  onDragArm: () => void
  onDragOver: (e: DragEvent) => void
  onDrop: () => void
  onDragEnd: () => void
}

/** One numbered shot: its description, what it builds on, and its last result. */
export function ShotCard({ shots, index, mode, maxChars, result, disabled, canAdd, dragging, onText, onRef, onMove, onDuplicate, onRemove, onDragArm, onDragOver, onDrop, onDragEnd }: Props) {
  const { t } = useTranslation('create')
  const refId = useId()
  const shot = shots[index]
  const n = index + 1
  const title = t('sequence.shot', { n })
  const shown = effectiveRef(shots, index, mode)
  const target = shot.ref ? shots.find((s) => s.id === shot.ref) : undefined
  const emptyTarget = target && !target.text.trim() ? shots.indexOf(target) + 1 : null

  const onKeyDown = (e: KeyboardEvent) => {
    if (!e.altKey || (e.key !== 'ArrowUp' && e.key !== 'ArrowDown')) return
    e.preventDefault()
    const to = e.key === 'ArrowUp' ? index - 1 : index + 1
    if (to >= 0 && to < shots.length) onMove(to)
  }

  return (
    <li
      draggable={dragging}
      onDragOver={onDragOver}
      onDrop={(e) => {
        e.preventDefault()
        onDrop()
      }}
      onDragEnd={onDragEnd}
      onKeyDown={onKeyDown}
      aria-label={title}
      className="flex min-w-0 flex-col gap-3 rounded-card border border-border bg-surface p-3"
    >
      <div className="flex items-center gap-1">
        <button
          type="button"
          aria-label={t('sequence.move.handle', { n })}
          title={t('sequence.move.hint')}
          onPointerDown={onDragArm}
          disabled={disabled}
          className="inline-flex size-8 cursor-grab items-center justify-center rounded-[8px] text-fg-muted hover:bg-surface-2 hover:text-fg active:cursor-grabbing disabled:cursor-not-allowed"
        >
          <GripVertical aria-hidden className="size-4" />
        </button>
        <h3 className="flex-1 text-body font-semibold text-fg">{title}</h3>
        <IconButton size="sm" label={t('sequence.move.up')} icon={<ArrowUp className="size-4" />} disabled={disabled || index === 0} onClick={() => onMove(index - 1)} />
        <IconButton size="sm" label={t('sequence.move.down')} icon={<ArrowDown className="size-4" />} disabled={disabled || index === shots.length - 1} onClick={() => onMove(index + 1)} />
        <Menu
          trigger={<IconButton size="sm" label={t('sequence.more', { n })} icon={<MoreHorizontal className="size-4" />} disabled={disabled} />}
          items={[
            { key: 'duplicate', label: t('sequence.duplicate'), icon: <Copy className="size-4" />, onSelect: onDuplicate, disabled: !canAdd },
            { key: 'remove', label: t('sequence.remove'), icon: <Trash2 className="size-4" />, onSelect: onRemove, danger: true, disabled: shots.length <= 1 },
          ]}
        />
      </div>

      <div className="relative flex aspect-[3/2] items-center justify-center overflow-hidden rounded-thumb border border-dashed border-border-control bg-surface-2">
        {result?.assetId ? (
          <ResultImage assetId={result.assetId} label={title} />
        ) : (
          <span className="flex flex-col items-center gap-1 text-caption text-fg-muted">
            <ImageIcon aria-hidden className="size-6" />
            {t('sequence.placeholder')}
          </span>
        )}
      </div>
      {result && (
        <div className="flex flex-wrap items-center gap-2 text-caption text-fg-muted">
          <span>{t('sequence.lastResult')}</span>
          <StatusPill status={stepPillStatus(result.status)} />
          {result.status === 'failed' && result.errorCode && <span className="w-full text-danger-fg">{failureText(t, result.errorCode)}</span>}
        </div>
      )}

      <Textarea
        aria-label={title}
        value={shot.text}
        maxChars={maxChars}
        placeholder={t('sequence.shotPlaceholder')}
        disabled={disabled}
        onChange={(e) => onText(e.target.value)}
        className="min-h-24"
      />

      <div className="flex flex-col gap-1">
        <label htmlFor={refId} className="text-caption font-medium text-fg">
          {t('sequence.ref.label')}
        </label>
        {index === 0 ? (
          <p className="text-caption text-fg-muted">{t('sequence.ref.first')}</p>
        ) : (
          <select
            id={refId}
            aria-label={t('sequence.ref.aria', { n })}
            value={shot.ref ?? ''}
            disabled={disabled}
            onChange={(e) => onRef(e.target.value || null)}
            className={cn(
              'h-9 w-full rounded-card border border-border-control bg-surface px-2.5 text-body text-fg',
              shown !== null && 'border-primary text-primary-text',
            )}
          >
            <option value="">{mode === 'continuity' ? t('sequence.ref.auto') : t('sequence.ref.none')}</option>
            {shots.map((s, i) =>
              i === index ? null : (
                <option key={s.id} value={s.id} disabled={i > index}>
                  {i < index ? t('sequence.ref.option', { n: i + 1 }) : t('sequence.ref.later', { n: i + 1 })}
                </option>
              ),
            )}
          </select>
        )}
        {emptyTarget !== null && <p className="text-caption text-warning-fg">{t('sequence.ref.emptyTarget', { n: emptyTarget })}</p>}
      </div>
    </li>
  )
}
