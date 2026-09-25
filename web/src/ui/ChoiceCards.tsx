import { useId, type ReactNode } from 'react'
import { CircleCheck } from 'lucide-react'
import { cn } from './cn'

export interface Choice<T extends string> {
  value: T
  title: ReactNode
  description?: ReactNode
  /** Extra line under the description, such as a price. */
  meta?: ReactNode
  disabled?: boolean
}

/** A radio group drawn as cards (fast/continuity, preview/direct, keep/redo/upgrade). */
export function ChoiceCards<T extends string>({
  name,
  value,
  onChange,
  choices,
  label,
  columns = 2,
  className,
}: {
  name?: string
  value: T
  onChange: (v: T) => void
  choices: Choice<T>[]
  label: string
  columns?: 1 | 2 | 3
  className?: string
}) {
  const auto = useId()
  const group = name ?? auto
  return (
    <div
      role="radiogroup"
      aria-label={label}
      className={cn('grid gap-3', columns === 1 ? 'grid-cols-1' : columns === 2 ? 'grid-cols-1 sm:grid-cols-2' : 'grid-cols-1 sm:grid-cols-3', className)}
    >
      {choices.map((c) => {
        const checked = c.value === value
        return (
          <label
            key={c.value}
            className={cn(
              'relative flex cursor-pointer flex-col gap-1 rounded-card border bg-surface p-3.5 transition-colors',
              'has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-primary',
              checked ? 'border-2 border-primary bg-primary-soft p-[13px]' : 'border-border-control hover:bg-surface-2',
              c.disabled && 'cursor-not-allowed opacity-45',
            )}
          >
            <input
              type="radio"
              name={group}
              value={c.value}
              checked={checked}
              disabled={c.disabled}
              onChange={() => onChange(c.value)}
              className="sr-only"
            />
            <span className="flex items-start justify-between gap-2">
              <span className={cn('text-body font-semibold', checked ? 'text-primary-text' : 'text-fg')}>{c.title}</span>
              {checked && <CircleCheck aria-hidden className="size-4.5 shrink-0 text-primary" />}
            </span>
            {c.description && <span className="text-caption text-fg-muted">{c.description}</span>}
            {c.meta && <span className="text-caption font-medium text-fg">{c.meta}</span>}
          </label>
        )
      })}
    </div>
  )
}
