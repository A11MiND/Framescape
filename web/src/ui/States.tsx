import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { CircleAlert, Inbox } from 'lucide-react'
import { Button } from './Button'
import { cn } from './cn'

export function EmptyState({ icon, title, body, action, className }: { icon?: ReactNode; title: ReactNode; body?: ReactNode; action?: ReactNode; className?: string }) {
  return (
    <div className={cn('flex flex-col items-center px-6 py-12 text-center', className)}>
      <div aria-hidden className="mb-4 flex size-16 items-center justify-center rounded-full bg-primary-soft text-primary-text">
        {icon ?? <Inbox className="size-7" />}
      </div>
      <h3 className="text-section font-semibold text-fg">{title}</h3>
      {body && <p className="mt-1 max-w-[360px] text-body text-fg-muted">{body}</p>}
      {action && <div className="mt-5">{action}</div>}
    </div>
  )
}

/** A failed load with a localized message and a retry action. */
export function ErrorState({ message, onRetry, className }: { message: ReactNode; onRetry?: () => void; className?: string }) {
  const { t } = useTranslation('ui')
  return (
    <div role="alert" className={cn('flex flex-col items-center px-6 py-12 text-center', className)}>
      <CircleAlert aria-hidden className="mb-3 size-10 text-danger-icon" />
      <h3 className="text-section font-semibold text-fg">{t('state.loadFailed')}</h3>
      <p className="mt-1 max-w-[360px] text-body text-fg-muted">{message}</p>
      {onRetry && (
        <Button className="mt-5" onClick={onRetry}>
          {t('action.retry')}
        </Button>
      )}
    </div>
  )
}

export function Skeleton({ className }: { className?: string }) {
  return <div aria-hidden className={cn('animate-pulse rounded-card bg-surface-2', className)} />
}
