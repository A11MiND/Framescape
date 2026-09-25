import * as ToggleGroup from '@radix-ui/react-toggle-group'
import type { ReactNode } from 'react'
import { cn } from './cn'

export interface SegmentOption<T extends string> {
  value: T
  label: ReactNode
  disabled?: boolean
}

export interface SegmentedControlProps<T extends string> {
  value: T
  onChange: (value: T) => void
  options: SegmentOption<T>[]
  /** Accessible name of the group. */
  label: string
  className?: string
  fullWidth?: boolean
}

/** One choice out of a few; arrow keys move between options. */
export function SegmentedControl<T extends string>({ value, onChange, options, label, className, fullWidth }: SegmentedControlProps<T>) {
  return (
    <ToggleGroup.Root
      type="single"
      value={value}
      onValueChange={(v) => v && onChange(v as T)}
      aria-label={label}
      className={cn('inline-flex h-9 items-stretch gap-0.5 rounded-card bg-surface-2 p-0.5', fullWidth && 'flex w-full', className)}
    >
      {options.map((o) => (
        <ToggleGroup.Item
          key={o.value}
          value={o.value}
          disabled={o.disabled}
          className={cn(
            'inline-flex items-center justify-center gap-1.5 rounded-[8px] text-body font-medium text-fg-muted transition-colors',
            'hover:text-fg data-[state=on]:bg-surface data-[state=on]:text-fg data-[state=on]:shadow-sm',
            'disabled:cursor-not-allowed disabled:opacity-45',
            fullWidth ? 'min-w-0 flex-1 px-1' : 'min-w-9 px-3',
          )}
        >
          {o.label}
        </ToggleGroup.Item>
      ))}
    </ToggleGroup.Root>
  )
}
