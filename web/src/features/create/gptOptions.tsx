import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Lock, TriangleAlert } from 'lucide-react'
import { Button, Dialog, EmptyState, SegmentedControl } from '../../ui'
import type { OpenAICapabilities } from '../../lib/api/create'
import type { SubmitNotice } from './useSubmit'
import { useSizeLabel } from './gptSizes'

// Shared by every mode that can generate with GPT Image.

/** A size choice drawn as its aspect ratio, so shapes compare at a glance. */
export function SizeOption({ size, label }: { size: string; label: string }) {
  const [w, h] = size.split('x').map(Number)
  const scale = 14 / Math.max(w || 1, h || 1)
  return (
    <span className="inline-flex items-center gap-1.5">
      {w > 0 && h > 0 && <span aria-hidden className="rounded-[2px] border-[1.5px] border-current" style={{ width: Math.round(w * scale), height: Math.round(h * scale) }} />}
      <span className="truncate">{label}</span>
    </span>
  )
}

export function GptAccess({ openai, entitled }: { openai?: OpenAICapabilities; entitled: boolean }) {
  const { t } = useTranslation('create')
  if (!openai?.enabled) {
    return <EmptyState icon={<TriangleAlert className="size-7" />} title={t('gpt.unavailable.title')} body={t('gpt.unavailable.body')} />
  }
  if (!entitled) return <EmptyState icon={<Lock className="size-7" />} title={t('gpt.notEnabled.title')} body={t('gpt.notEnabled.body')} />
  return null
}

/** Output size, drawn by aspect ratio; the sizes come from capabilities. */
export function GptSizeControl({ sizes, value, onChange }: { sizes: string[]; value: string; onChange: (size: string) => void }) {
  const { t } = useTranslation('create')
  const sizeLabel = useSizeLabel()
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-label font-medium text-fg">{t('gpt.size')}</span>
      <SegmentedControl fullWidth label={t('gpt.size')} value={value} onChange={onChange} options={sizes.map((s) => ({ value: s, label: <SizeOption size={s} label={sizeLabel(s)} /> }))} />
    </div>
  )
}

interface ConfirmJob {
  submitting: boolean
  canSubmit: boolean
  submit: () => void
  notice: SubmitNotice
}

/** Reviewing the cost before a GPT generation: what is sent and what is reserved. */
export function GptConfirm({ open, onOpenChange, rows, job, children }: { open: boolean; onOpenChange: (o: boolean) => void; rows: [string, string][]; job: ConfirmJob; children?: ReactNode }) {
  const { t } = useTranslation('create')
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('confirm.title')}
      locked={job.submitting}
      footer={
        <>
          <Button onClick={() => onOpenChange(false)} disabled={job.submitting}>
            {t('ui:action.cancel')}
          </Button>
          <Button variant="primary" loading={job.submitting} disabled={!job.canSubmit && !job.submitting} onClick={job.submit}>
            {t('action.confirm')}
          </Button>
        </>
      }
    >
      <dl className="flex flex-col gap-2">
        {rows.map(([k, v]) => (
          <div key={k} className="flex justify-between gap-3 text-body">
            <dt className="text-fg-muted">{k}</dt>
            <dd className="text-right font-medium text-fg tabular-nums">{v}</dd>
          </div>
        ))}
      </dl>
      {children}
      <p className="mt-3 text-caption text-fg-muted">{t('cost.noteUsage')}</p>
      {job.notice && (
        <p role="alert" className="mt-2 text-caption text-warning-fg">
          {job.notice === 'priceChanged' ? t('error.priceChanged') : t('error.insufficient')}
        </p>
      )}
    </Dialog>
  )
}
