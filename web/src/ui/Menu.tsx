import * as Dropdown from '@radix-ui/react-dropdown-menu'
import type { ReactNode } from 'react'
import { cn } from './cn'

export interface MenuItem {
  key: string
  label: ReactNode
  icon?: ReactNode
  onSelect: () => void
  danger?: boolean
  disabled?: boolean
}

/** A row or header action menu. Danger items are grouped last behind a separator. */
export function Menu({ trigger, items, align = 'end' }: { trigger: ReactNode; items: MenuItem[]; align?: 'start' | 'end' }) {
  const normal = items.filter((i) => !i.danger)
  const danger = items.filter((i) => i.danger)
  const item = (i: MenuItem) => (
    <Dropdown.Item
      key={i.key}
      disabled={i.disabled}
      onSelect={i.onSelect}
      className={cn(
        'flex h-9 cursor-pointer items-center gap-2 rounded-[8px] px-2.5 text-body outline-none select-none',
        'data-[disabled]:cursor-not-allowed data-[disabled]:opacity-45',
        i.danger ? 'text-danger-fg data-[highlighted]:bg-danger-soft' : 'text-fg data-[highlighted]:bg-surface-2',
      )}
    >
      {i.icon && <span aria-hidden className="inline-flex size-4 items-center justify-center">{i.icon}</span>}
      {i.label}
    </Dropdown.Item>
  )
  return (
    <Dropdown.Root>
      <Dropdown.Trigger asChild>{trigger}</Dropdown.Trigger>
      <Dropdown.Portal>
        <Dropdown.Content
          align={align}
          sideOffset={4}
          className="z-50 min-w-[200px] rounded-card border border-border bg-surface p-1 shadow-overlay"
        >
          {normal.map(item)}
          {normal.length > 0 && danger.length > 0 && <Dropdown.Separator className="my-1 h-px bg-border" />}
          {danger.map(item)}
        </Dropdown.Content>
      </Dropdown.Portal>
    </Dropdown.Root>
  )
}
