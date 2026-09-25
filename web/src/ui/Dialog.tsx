import * as RadixDialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from './Button'
import { cn } from './cn'
import { useReturnFocus } from './useReturnFocus'

// Radix keeps focus inside the dialog, closes on Escape and returns focus
// to the element that opened it.

export interface DialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  children?: ReactNode
  footer?: ReactNode
  size?: 'confirm' | 'form'
  /** Blocks closing by Escape or overlay click, e.g. while submitting. */
  locked?: boolean
}

export function Dialog({ open, onOpenChange, title, description, children, footer, size = 'confirm', locked = false }: DialogProps) {
  const { t } = useTranslation('ui')
  const returnFocus = useReturnFocus(open)
  return (
    <RadixDialog.Root open={open} onOpenChange={(o) => (!locked || o) && onOpenChange(o)}>
      <RadixDialog.Portal>
        <RadixDialog.Overlay className="fixed inset-0 z-50 bg-overlay" />
        <RadixDialog.Content
          onEscapeKeyDown={(e) => locked && e.preventDefault()}
          onCloseAutoFocus={returnFocus}
          onPointerDownOutside={(e) => locked && e.preventDefault()}
          className={cn(
            'fixed top-1/2 left-1/2 z-50 flex max-h-[calc(100dvh-32px)] w-[calc(100vw-32px)] -translate-x-1/2 -translate-y-1/2 flex-col',
            'rounded-dialog border border-border bg-surface shadow-overlay',
            size === 'confirm' ? 'max-w-[480px]' : 'max-w-[640px]',
          )}
        >
          <div className="flex items-start justify-between gap-4 px-6 pt-5">
            <div className="min-w-0">
              <RadixDialog.Title className="text-[18px] leading-7 font-semibold text-fg">{title}</RadixDialog.Title>
              {description ? (
                <RadixDialog.Description className="mt-1 text-body text-fg-muted">{description}</RadixDialog.Description>
              ) : (
                <RadixDialog.Description className="sr-only">{title}</RadixDialog.Description>
              )}
            </div>
            <RadixDialog.Close asChild disabled={locked}>
              <button
                type="button"
                aria-label={t('action.close')}
                className="-mr-2 inline-flex size-8 shrink-0 items-center justify-center rounded-card text-fg-muted hover:bg-surface-2 hover:text-fg disabled:opacity-45"
              >
                <X aria-hidden className="size-4" />
              </button>
            </RadixDialog.Close>
          </div>
          {children && <div className="min-h-0 overflow-y-auto px-6 pt-4">{children}</div>}
          {footer && <div className="flex flex-wrap justify-end gap-2 px-6 pt-5 pb-5">{footer}</div>}
          {!footer && <div className="pb-5" />}
        </RadixDialog.Content>
      </RadixDialog.Portal>
    </RadixDialog.Root>
  )
}

export interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  /** Who or what the action applies to. */
  target?: ReactNode
  /** What changes once confirmed. */
  effects?: ReactNode[]
  body?: ReactNode
  confirmLabel: ReactNode
  onConfirm: () => void
  danger?: boolean
  busy?: boolean
}

/** Confirmation for consequential actions: names the target and the effects. */
export function ConfirmDialog({ open, onOpenChange, title, target, effects, body, confirmLabel, onConfirm, danger = false, busy = false }: ConfirmDialogProps) {
  const { t } = useTranslation('ui')
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      locked={busy}
      footer={
        <>
          <Button onClick={() => onOpenChange(false)} disabled={busy}>
            {t('action.cancel')}
          </Button>
          <Button variant={danger ? 'danger' : 'primary'} loading={busy} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3 text-body text-fg">
        {target && (
          <div className="flex gap-3">
            <span className="w-16 shrink-0 text-fg-muted">{t('confirm.target')}</span>
            <span className="min-w-0 font-medium break-words">{target}</span>
          </div>
        )}
        {effects && effects.length > 0 && (
          <div className="flex gap-3">
            <span className="w-16 shrink-0 text-fg-muted">{t('confirm.effects')}</span>
            <ul className="min-w-0 list-disc space-y-1 pl-4">
              {effects.map((e, i) => (
                <li key={i}>{e}</li>
              ))}
            </ul>
          </div>
        )}
        {body}
      </div>
    </Dialog>
  )
}
