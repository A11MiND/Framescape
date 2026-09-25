import type { HTMLAttributes, ReactNode } from 'react'
import { cn } from './cn'

export function Card({ padding = 'md', className, ...rest }: HTMLAttributes<HTMLDivElement> & { padding?: 'sm' | 'md' | 'lg' | 'none' }) {
  const pad = { none: '', sm: 'p-3', md: 'p-4', lg: 'p-6' }[padding]
  return <div className={cn('rounded-card border border-border bg-surface', pad, className)} {...rest} />
}

/** A section heading with optional trailing actions. */
export function SectionHeader({ title, description, actions, className }: { title: ReactNode; description?: ReactNode; actions?: ReactNode; className?: string }) {
  return (
    <div className={cn('flex flex-wrap items-start justify-between gap-3', className)}>
      <div className="min-w-0">
        <h2 className="text-section font-semibold text-fg">{title}</h2>
        {description && <p className="mt-0.5 text-caption text-fg-muted">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  )
}

/** A page's title row: title, one-line description and actions. */
export function PageHeader({ title, description, actions }: { title: ReactNode; description?: ReactNode; actions?: ReactNode }) {
  return (
    <header className="flex flex-wrap items-end justify-between gap-4">
      <div className="min-w-0">
        <h1 className="text-title font-semibold text-balance text-fg">{title}</h1>
        {description && <p className="mt-1 max-w-[68ch] text-body text-fg-muted">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </header>
  )
}
