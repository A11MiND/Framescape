import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Clapperboard, Film, GalleryHorizontal, Image as ImageIcon, LayoutGrid, type LucideIcon } from 'lucide-react'
import { StatusPill, buttonClasses, cn } from '../../ui'
import { formatDateTime, formatNumber } from '../../lib/format'
import { failureText } from '../../lib/errorText'
import type { JobListItem } from '../../lib/api/jobs'
import { TaskActions } from './TaskActions'

const TYPE_ICON: Record<string, LucideIcon> = {
  'image.single': ImageIcon,
  'image.sequence': GalleryHorizontal,
  'image.comic4': LayoutGrid,
  'video.single': Clapperboard,
  'video.sequence': Film,
}

function typeKey(workflow: string) {
  return workflow in TYPE_ICON ? workflow : 'unknown'
}

function Thumb({ job }: { job: JobListItem }) {
  const Icon = TYPE_ICON[job.workflow_name] ?? ImageIcon
  return job.cover_url ? (
    <img src={job.cover_url} alt="" loading="lazy" className="size-12 shrink-0 rounded-thumb bg-surface-2 object-cover" />
  ) : (
    <span aria-hidden className="flex size-12 shrink-0 items-center justify-center rounded-thumb bg-surface-2 text-fg-muted">
      <Icon className="size-5" />
    </span>
  )
}

function useItem(job: JobListItem) {
  const { t, i18n } = useTranslation('tasks')
  const title = job.title || t(`type.${typeKey(job.workflow_name)}`) || t('untitled')
  const failed = job.status === 'failed' || job.status === 'partial'
  const primary =
    job.status === 'awaiting_review'
      ? { label: t('action.review'), variant: 'primary' as const }
      : failed
        ? { label: t('action.reason'), variant: 'secondary' as const }
        : { label: t('action.view'), variant: 'secondary' as const }
  const n = (v: number) => formatNumber(v, i18n.language)
  return {
    t,
    title,
    failed,
    primary,
    type: t(`type.${typeKey(job.workflow_name)}`),
    credits: t('credits', { reserved: n(job.credits.reserved), settled: n(job.credits.settled) }),
    progress: job.node_total > 0 ? t('progress', { done: job.node_done, total: job.node_total }) : t('ui:unknownValue'),
    created: formatDateTime(job.created_at, i18n.language),
    reason: failed ? failureText(t, job.error_code) : null,
  }
}

function TitleBlock({ job, title, reason, retryLabel }: { job: JobListItem; title: string; reason: string | null; retryLabel: string }) {
  return (
    <div className="min-w-0">
      <Link to={`/jobs/${job.biz_id}`} className="block truncate text-body font-medium text-fg hover:underline">
        {title}
      </Link>
      {reason && <p className="truncate text-caption text-danger-fg">{reason}</p>}
      {!reason && job.retry_of_job_id && (
        <Link to={`/jobs/${job.retry_of_job_id}`} className="text-caption text-fg-muted hover:underline">
          {retryLabel}
        </Link>
      )}
    </div>
  )
}

/** A table row on desktop. */
export function TaskRow({ job, highlight }: { job: JobListItem; highlight?: boolean }) {
  const v = useItem(job)
  const review = job.status === 'awaiting_review'
  return (
    <tr className={cn('border-b border-border last:border-b-0 hover:bg-surface-2', highlight && 'bg-primary-soft', review && 'shadow-[inset_3px_0_0_var(--warning)]')}>
      <td className="py-2 pr-3 pl-4">
        <div className="flex items-center gap-3">
          <Thumb job={job} />
          <TitleBlock job={job} title={v.title} reason={v.reason} retryLabel={v.t('retryOf')} />
        </div>
      </td>
      <td className="px-3 text-body whitespace-nowrap text-fg-muted">{v.type}</td>
      <td className="px-3 text-body whitespace-nowrap text-fg-muted tabular-nums">{v.progress}</td>
      <td className="px-3">
        <StatusPill status={job.status} />
      </td>
      <td className="px-3 text-caption whitespace-nowrap text-fg tabular-nums">{v.credits}</td>
      <td className="px-3 text-caption whitespace-nowrap text-fg-muted tabular-nums">{v.created}</td>
      <td className="py-2 pr-4 pl-3">
        <div className="flex items-center justify-end gap-1">
          <Link to={`/jobs/${job.biz_id}`} className={buttonClasses(v.primary.variant, 'sm')}>
            {v.primary.label}
          </Link>
          <TaskActions job={job} title={v.title} />
        </div>
      </td>
    </tr>
  )
}

/** A card below 768px. */
export function TaskCard({ job, highlight }: { job: JobListItem; highlight?: boolean }) {
  const v = useItem(job)
  return (
    <li
      className={cn(
        'flex gap-3 rounded-card border border-border bg-surface p-3',
        highlight && 'bg-primary-soft',
        job.status === 'awaiting_review' && 'border-l-4 border-l-warning',
      )}
    >
      <Thumb job={job} />
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div className="flex items-start justify-between gap-2">
          <TitleBlock job={job} title={v.title} reason={v.reason} retryLabel={v.t('retryOf')} />
          <TaskActions job={job} title={v.title} />
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-fg-muted">
          <StatusPill status={job.status} />
          <span>{v.type}</span>
          <span className="tabular-nums">{v.credits}</span>
        </div>
        <div className="flex items-center justify-between gap-2">
          <span className="text-caption text-fg-muted tabular-nums">{v.created}</span>
          <Link to={`/jobs/${job.biz_id}`} className="text-caption font-medium text-primary-text hover:underline">
            {v.primary.label}
          </Link>
        </div>
      </div>
    </li>
  )
}
