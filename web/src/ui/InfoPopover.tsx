import * as Popover from '@radix-ui/react-popover'
import { Info } from 'lucide-react'
import type { ReactNode } from 'react'

/** An explanation opened by click or keyboard, not hover only (cost notes, reservation rules). */
export function InfoPopover({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Popover.Root>
      <Popover.Trigger asChild>
        <button type="button" aria-label={label} className="inline-flex size-6 items-center justify-center rounded-full text-fg-muted hover:bg-surface-2 hover:text-fg">
          <Info aria-hidden className="size-4" />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content sideOffset={6} className="z-50 max-w-[320px] rounded-card border border-border bg-surface p-3 text-caption text-fg shadow-overlay">
          {children}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}
