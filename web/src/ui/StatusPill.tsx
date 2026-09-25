import { CircleCheck, CircleMinus, CircleX, Clock, LoaderCircle, CirclePause, TriangleAlert, type LucideIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from './cn'
import { isJobStatus, type JobStatus } from './status'

type Tone = 'neutral' | 'primary' | 'warning' | 'success' | 'danger'

// Spec section 4: every status has an icon, a label and a color.
const STATUS: Record<JobStatus, { tone: Tone; icon: LucideIcon; spin?: boolean }> = {
  queued: { tone: 'neutral', icon: Clock },
  running: { tone: 'primary', icon: LoaderCircle, spin: true },
  awaiting_review: { tone: 'warning', icon: CirclePause },
  succeeded: { tone: 'success', icon: CircleCheck },
  partial: { tone: 'warning', icon: TriangleAlert },
  failed: { tone: 'danger', icon: CircleX },
  cancelling: { tone: 'neutral', icon: LoaderCircle, spin: true },
  cancelled: { tone: 'neutral', icon: CircleMinus },
}

const TONES: Record<Tone, { pill: string; icon: string }> = {
  neutral: { pill: 'bg-surface-2 text-fg-muted', icon: 'text-fg-muted' },
  primary: { pill: 'bg-primary-soft text-primary-text', icon: 'text-primary-text' },
  warning: { pill: 'bg-warning-soft text-warning-fg', icon: 'text-warning' },
  success: { pill: 'bg-success-soft text-success-fg', icon: 'text-success' },
  danger: { pill: 'bg-danger-soft text-danger-fg', icon: 'text-danger-icon' },
}

export function StatusPill({ status, className }: { status: string; className?: string }) {
  const { t } = useTranslation('ui')
  const def = isJobStatus(status) ? STATUS[status] : { tone: 'neutral' as Tone, icon: Clock }
  const tone = TONES[def.tone]
  const Icon = def.icon
  return (
    <span className={cn('inline-flex h-6 items-center gap-1.5 rounded-full px-2 text-caption font-medium whitespace-nowrap', tone.pill, className)}>
      <Icon aria-hidden className={cn('size-3.5', tone.icon, 'spin' in def && def.spin && 'animate-spin')} />
      {isJobStatus(status) ? t(`status.${status}`) : t('status.unknown')}
    </span>
  )
}
