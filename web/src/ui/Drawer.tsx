import * as RadixDialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from './cn'
import { useReturnFocus } from './useReturnFocus'

export interface DrawerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  children: ReactNode
  footer?: ReactNode
  /** Width on desktop; below 1024px the drawer becomes a bottom sheet. */
  width?: 'drawer' | 'inspector'
}

/** Right-side drawer on desktop, bottom sheet below 1024px. Scrolls inside and always has a close button. */
export function Drawer({ open, onOpenChange, title, description, children, footer, width = 'drawer' }: DrawerProps) {
  const { t } = useTranslation('ui')
  const returnFocus = useReturnFocus(open)
  return (
    <RadixDialog.Root open={open} onOpenChange={onOpenChange}>
      <RadixDialog.Portal>
        <RadixDialog.Overlay className="fixed inset-0 z-50 bg-overlay" />
        <RadixDialog.Content
          onCloseAutoFocus={returnFocus}
          className={cn(
            'fixed z-50 flex flex-col bg-surface shadow-overlay',
            'inset-x-0 bottom-0 max-h-[90dvh] rounded-t-dialog pb-[env(safe-area-inset-bottom)]',
            'lg:inset-y-0 lg:right-0 lg:left-auto lg:max-h-none lg:rounded-none lg:border-l lg:border-border lg:pb-0',
            width === 'drawer' ? 'lg:w-drawer' : 'lg:w-inspector',
          )}
        >
          <div aria-hidden className="mx-auto mt-2 h-1 w-10 shrink-0 rounded-full bg-border lg:hidden" />
          <div className="flex h-14 shrink-0 items-center justify-between gap-3 border-b border-border px-5">
            <div className="min-w-0">
              <RadixDialog.Title className="truncate text-section font-semibold text-fg">{title}</RadixDialog.Title>
              {description ? (
                <RadixDialog.Description className="truncate text-caption text-fg-muted">{description}</RadixDialog.Description>
              ) : (
                <RadixDialog.Description className="sr-only">{title}</RadixDialog.Description>
              )}
            </div>
            <RadixDialog.Close asChild>
              <button
                type="button"
                aria-label={t('action.close')}
                className="-mr-2 inline-flex size-10 items-center justify-center rounded-card text-fg-muted hover:bg-surface-2 hover:text-fg"
              >
                <X aria-hidden className="size-5" />
              </button>
            </RadixDialog.Close>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 py-4">{children}</div>
          {footer && <div className="flex shrink-0 flex-wrap justify-end gap-2 border-t border-border px-5 py-3">{footer}</div>}
        </RadixDialog.Content>
      </RadixDialog.Portal>
    </RadixDialog.Root>
  )
}
