import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { formatDuration, formatClock } from '../lib/format'
import { cn } from './cn'

/** Progress of unknown length. Generation never shows a made-up percentage. */
export function IndeterminateProgress({ label, className }: { label: string; className?: string }) {
  return (
    <div role="progressbar" aria-label={label} aria-busy="true" className={cn('relative h-1 w-full overflow-hidden rounded-full bg-surface-2', className)}>
      <div className="absolute inset-y-0 w-1/3 animate-[indeterminate_1.4s_ease-in-out_infinite] rounded-full bg-primary motion-reduce:w-full motion-reduce:animate-none motion-reduce:opacity-40" />
    </div>
  )
}

/** Real progress, used only where the number is measured (uploads). */
export function DeterminateProgress({ value, label, className }: { value: number; label: string; className?: string }) {
  const pct = Math.max(0, Math.min(100, Math.round(value)))
  return (
    <div role="progressbar" aria-label={label} aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100} className={cn('h-1 w-full overflow-hidden rounded-full bg-surface-2', className)}>
      <div className="h-full rounded-full bg-primary transition-[width]" style={{ width: `${pct}%` }} />
    </div>
  )
}

function useNow(active: boolean) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [active])
  return now
}

/** "Elapsed 1m 12s", ticking until `end` is set. */
export function ElapsedTime({ start, end, className }: { start: string | Date; end?: string | Date | null; className?: string }) {
  const { t } = useTranslation('ui')
  const now = useNow(!end)
  const from = new Date(start).getTime()
  const to = end ? new Date(end).getTime() : now
  return <span className={cn('tabular-nums', className)}>{t('time.elapsed', { duration: formatDuration(to - from, t) })}</span>
}

/** "Last updated 14:20:05", the time the stream or last poll was fresh. */
export function LastSync({ at, className }: { at: Date | null; className?: string }) {
  const { t, i18n } = useTranslation('ui')
  if (!at) return null
  return <span className={cn('tabular-nums', className)}>{t('time.lastSync', { time: formatClock(at, i18n.language) })}</span>
}
