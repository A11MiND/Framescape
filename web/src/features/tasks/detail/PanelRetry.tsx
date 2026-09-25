import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Dialog, Field, Skeleton, Textarea } from '../../../ui'
import { jobsApi } from '../../../lib/api/jobs'
import { keys } from '../../../lib/api/keys'
import { ApiError } from '../../../lib/api/client'
import { errorText } from '../../../lib/errorText'
import { formatNumber } from '../../../lib/format'
import { useToast } from '../../../components/Toast'

/** Redraws a classic comic's unfinished panels, with editable text, as a new linked task. */
export function PanelRetry({ bizId }: { bizId: string }) {
  const { t, i18n } = useTranslation('job')
  const navigate = useNavigate()
  const qc = useQueryClient()
  const toast = useToast()
  const [open, setOpen] = useState(false)
  const [texts, setTexts] = useState<Record<number, string>>({})
  const [notice, setNotice] = useState<string | null>(null)
  const quote = useQuery({ queryKey: [...keys.jobs.detail(bizId), 'panel-retry-quote'], queryFn: () => jobsApi.quoteComicRetry(bizId), enabled: open, retry: false })

  const retry = useMutation({
    mutationFn: () => {
      const changed: Record<number, string> = {}
      for (const p of quote.data?.panels ?? []) {
        const v = texts[p.index]?.trim()
        if (v && v !== p.text.trim()) changed[p.index] = v
      }
      return jobsApi.retryComicPanels(bizId, changed, quote.data!.credits_total)
    },
    onSuccess: (res) => {
      setOpen(false)
      toast(t('panelRetry.done'))
      qc.invalidateQueries({ queryKey: keys.jobs.all })
      navigate(`/jobs/${res.biz_id}`)
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === 'price_changed') {
        setNotice(t('review.priceChanged'))
        quote.refetch()
      } else {
        setNotice(errorText(t, err))
      }
    },
  })

  const n = quote.data?.credits_total
  return (
    <>
      <Button
        onClick={() => {
          setTexts({})
          setNotice(null)
          setOpen(true)
        }}
      >
        {t('panelRetry.action')}
      </Button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={t('panelRetry.title')}
        description={t('panelRetry.body')}
        size="form"
        locked={retry.isPending}
        footer={
          <>
            <Button onClick={() => setOpen(false)} disabled={retry.isPending}>
              {t('ui:action.cancel')}
            </Button>
            <Button variant="primary" loading={retry.isPending} disabled={!quote.data || quote.isFetching} onClick={() => retry.mutate()}>
              {t('panelRetry.confirm')}
            </Button>
          </>
        }
      >
        {quote.isPending ? (
          <Skeleton className="h-40 w-full" />
        ) : quote.isError ? (
          <p role="alert" className="text-body text-danger-fg">
            {t('panelRetry.failed', { reason: errorText(t, quote.error) })}
          </p>
        ) : (
          <div className="flex flex-col gap-4">
            {quote.data.panels.map((p) => (
              <Field key={p.index} label={t('panelRetry.panel', { n: p.index })}>
                <Textarea value={texts[p.index] ?? p.text} maxChars={1500} disabled={retry.isPending} onChange={(e) => setTexts((cur) => ({ ...cur, [p.index]: e.target.value }))} />
              </Field>
            ))}
            <div className="flex flex-col gap-1 rounded-card bg-surface-2 p-3 text-body">
              <p className="font-semibold text-fg tabular-nums">{n === undefined ? t('panelRetry.costPending') : t('panelRetry.cost', { n: formatNumber(n, i18n.language) })}</p>
              <p className="text-caption text-fg-muted">{t('panelRetry.newTask')}</p>
            </div>
            {notice && (
              <p role="alert" className="text-caption text-warning-fg">
                {notice}
              </p>
            )}
          </div>
        )}
      </Dialog>
    </>
  )
}
