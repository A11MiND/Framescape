import { useTranslation } from 'react-i18next'
import { cn } from '../../ui'

export function Brand({ compact = false, className }: { compact?: boolean; className?: string }) {
  const { t } = useTranslation('shell')
  return (
    <span className={cn('flex items-center gap-2.5', className)}>
      <img src="/logo-mark.png" alt="" className="size-7 shrink-0 object-contain" />
      <span className={cn('text-[17px] leading-6 font-semibold text-fg', compact && 'sr-only')}>{t('brand')}</span>
    </span>
  )
}
