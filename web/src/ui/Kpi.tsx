import type { ReactNode } from 'react'
import { cn } from './cn'

export function Kpi({ label, value, note, icon, className }: { label: ReactNode; value: ReactNode; note?: ReactNode; icon?: ReactNode; className?: string }) {
  return (
    <div className={cn('flex gap-3 rounded-card border border-border bg-surface p-4', className)}>
      {icon && <div aria-hidden className="flex size-10 shrink-0 items-center justify-center rounded-card bg-primary-soft text-primary-text">{icon}</div>}
      <div className="min-w-0">
        <div className="text-caption text-fg-muted">{label}</div>
        <div className="text-kpi font-semibold text-fg tabular-nums">{value}</div>
        {note && <div className="text-caption text-fg-muted">{note}</div>}
      </div>
    </div>
  )
}
