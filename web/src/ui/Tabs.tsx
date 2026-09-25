import * as RadixTabs from '@radix-ui/react-tabs'
import type { ReactNode } from 'react'
import { cn } from './cn'

export const Tabs = RadixTabs.Root
export const TabPanel = RadixTabs.Content

export interface TabItem {
  value: string
  label: ReactNode
  icon?: ReactNode
  count?: number
}

/** Underlined tabs. `size="mode"` is the taller creation-mode variant. */
export function TabList({ items, label, size = 'page', className }: { items: TabItem[]; label: string; size?: 'page' | 'mode'; className?: string }) {
  return (
    <RadixTabs.List aria-label={label} className={cn('flex gap-1 border-b border-border', className)}>
      {items.map((it) => (
        <RadixTabs.Trigger
          key={it.value}
          value={it.value}
          className={cn(
            '-mb-px inline-flex items-center gap-2 border-b-2 border-transparent px-3 font-medium text-fg-muted transition-colors',
            'hover:text-fg data-[state=active]:border-primary data-[state=active]:text-primary-text',
            size === 'mode' ? 'h-12 text-[15px]' : 'h-10 text-body',
          )}
        >
          {it.icon}
          {it.label}
          {it.count !== undefined && (
            <span className="rounded-full bg-surface-2 px-1.5 text-badge font-medium tabular-nums text-fg-muted">{it.count}</span>
          )}
        </RadixTabs.Trigger>
      ))}
    </RadixTabs.List>
  )
}
