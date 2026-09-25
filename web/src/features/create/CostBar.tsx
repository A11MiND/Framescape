import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Sparkles } from 'lucide-react'
import { Button, InfoPopover, buttonClasses } from '../../ui'
import { formatNumber } from '../../lib/format'
import type { SubmitNotice } from './useSubmit'

interface Props {
  /** False while the request is incomplete: nothing to price yet. */
  ready?: boolean
  credits: number | undefined
  calculating: boolean
  failed: boolean
  /** GPT is settled from reported usage: explain that the reservation is not a cap. */
  usageBased?: boolean
  /** A line about what is being priced, such as how many images. */
  detail?: string
  notice: SubmitNotice
  actionLabel: string
  onAction: () => void
  loading: boolean
  disabled: boolean
}

/** The composer's footer: the server quote with its explanation and the one primary action. */
export function CostBar({ ready = true, credits, calculating, failed, usageBased, detail, notice, actionLabel, onAction, loading, disabled }: Props) {
  const { t, i18n } = useTranslation('create')
  const value = !ready ? t('cost.pending') : failed ? t('cost.unknown') : calculating || credits === undefined ? t('cost.calculating') : t('cost.value', { n: formatNumber(credits, i18n.language) })
  return (
    <div className="sticky bottom-0 z-10 -mx-4 mt-auto flex flex-col gap-2 border-t border-border bg-surface-2 px-4 py-3 lg:-mx-5 lg:px-5">
      {notice && (
        <p role="alert" className="flex flex-wrap items-center gap-2 text-caption text-warning-fg">
          {notice === 'priceChanged' ? t('error.priceChanged') : t('error.insufficient')}
          {notice === 'insufficient' && (
            <Link to="/credits" className={buttonClasses('ghost', 'sm')}>
              {t('error.topUp')}
            </Link>
          )}
        </p>
      )}
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-1">
          <div className="min-w-0">
            <div className="text-caption text-fg-muted">{t('cost.label')}</div>
            <div aria-live="polite" className={!ready ? 'text-body text-fg-muted' : failed ? 'text-body text-danger-fg' : 'text-section font-semibold text-fg tabular-nums'}>
              {value}
            </div>
            {detail && <div className="text-caption text-fg-muted">{detail}</div>}
          </div>
          <InfoPopover label={t('cost.explain')}>
            <p>{t('cost.note')}</p>
            {usageBased && <p className="mt-2">{t('cost.noteUsage')}</p>}
          </InfoPopover>
        </div>
        <Button variant="primary" size="lg" icon={<Sparkles aria-hidden className="size-5" />} loading={loading} disabled={disabled} onClick={onAction}>
          {actionLabel}
        </Button>
      </div>
    </div>
  )
}
