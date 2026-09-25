import { useRef, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { ImageUp, MessageSquare, Redo2, Stamp, Type, Undo2 } from 'lucide-react'
import { cn } from '../../ui'
import { COMIC_IMAGE_MIMES } from '../../lib/comicDocument'

interface Props {
  vertical: boolean
  canAdd: boolean
  canUndo: boolean
  canRedo: boolean
  busy: boolean
  pending: boolean
  onAdd: (kind: 'bubble' | 'text') => void
  onLogo: (file: File) => void
  onPage: (file: File) => void
  onUndo: () => void
  onRedo: () => void
}

function Tool({ label, icon, vertical, disabled, onClick }: { label: string; icon: ReactNode; vertical: boolean; disabled?: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={cn(
        'inline-flex items-center justify-center gap-1.5 rounded-card text-fg-muted transition-colors hover:bg-surface-2 hover:text-fg disabled:cursor-not-allowed disabled:opacity-45',
        vertical ? 'h-16 w-full flex-col text-badge' : 'h-9 px-2.5 text-caption',
      )}
    >
      <span aria-hidden className="inline-flex size-5 items-center justify-center">
        {icon}
      </span>
      {label}
    </button>
  )
}

/** Lettering tools: bubbles, titles, a logo, an imported page, undo and redo. */
export function Toolbar({ vertical, canAdd, canUndo, canRedo, busy, pending, onAdd, onLogo, onPage, onUndo, onRedo }: Props) {
  const { t } = useTranslation('comic')
  const logo = useRef<HTMLInputElement>(null)
  const page = useRef<HTMLInputElement>(null)
  const pick = (ref: React.RefObject<HTMLInputElement | null>) => ref.current?.click()
  return (
    <div role="toolbar" aria-label={t('toolbar.label')} aria-orientation={vertical ? 'vertical' : 'horizontal'} className={cn('flex gap-1', vertical ? 'w-[72px] flex-col rounded-card border border-border bg-surface p-1' : 'flex-wrap')}>
      <Tool vertical={vertical} label={t('toolbar.addBubble')} icon={<MessageSquare className="size-5" />} disabled={!canAdd} onClick={() => onAdd('bubble')} />
      <Tool vertical={vertical} label={t('toolbar.addText')} icon={<Type className="size-5" />} disabled={!canAdd} onClick={() => onAdd('text')} />
      <Tool vertical={vertical} label={t('toolbar.addLogo')} icon={<Stamp className="size-5" />} disabled={!canAdd || busy} onClick={() => pick(logo)} />
      <Tool vertical={vertical} label={t('toolbar.importPage')} icon={<ImageUp className="size-5" />} disabled={busy || pending} onClick={() => pick(page)} />
      <span aria-hidden className={cn('bg-border', vertical ? 'my-1 h-px w-full' : 'mx-1 w-px self-stretch')} />
      <Tool vertical={vertical} label={t('toolbar.undo')} icon={<Undo2 className="size-5" />} disabled={!canUndo} onClick={onUndo} />
      <Tool vertical={vertical} label={t('toolbar.redo')} icon={<Redo2 className="size-5" />} disabled={!canRedo} onClick={onRedo} />
      <input
        ref={logo}
        type="file"
        aria-label={t('toolbar.addLogo')}
        accept={COMIC_IMAGE_MIMES.join(',')}
        className="sr-only"
        tabIndex={-1}
        onChange={(e) => {
          const f = e.target.files?.[0]
          e.target.value = ''
          if (f) onLogo(f)
        }}
      />
      <input
        ref={page}
        type="file"
        aria-label={t('toolbar.importPage')}
        accept={COMIC_IMAGE_MIMES.join(',')}
        className="sr-only"
        tabIndex={-1}
        onChange={(e) => {
          const f = e.target.files?.[0]
          e.target.value = ''
          if (f) onPage(f)
        }}
      />
    </div>
  )
}
