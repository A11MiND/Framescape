import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CirclePause } from 'lucide-react'
import { Button, Card, Field, SegmentedControl, Skeleton, Textarea, buttonClasses, cn } from '../../../ui'
import { jobsApi, type JobDetail, type ReviewDecision } from '../../../lib/api/jobs'
import { assetsApi } from '../../../lib/api/assets'
import { keys } from '../../../lib/api/keys'
import { ApiError } from '../../../lib/api/client'
import { errorText } from '../../../lib/errorText'
import { formatDateTime, formatNumber } from '../../../lib/format'
import { useMe } from '../../../app/useMe'
import { useToast } from '../../../components/Toast'
import { draftShots } from './model'

type Choice = 'keep' | 'redo' | 'upgrade'

const VIDEO_PROMPT_MAX = 7000

function Clip({ assetId, label }: { assetId: string; label: string }) {
  const asset = useQuery({ queryKey: keys.assets.detail(assetId), queryFn: () => assetsApi.get(assetId), enabled: Boolean(assetId) })
  if (!asset.data) return <Skeleton className="aspect-video w-full" />
  return (
    <video
      src={asset.data.public_url}
      poster={asset.data.thumb_url || undefined}
      controls
      preload="metadata"
      aria-label={label}
      className="aspect-video w-full rounded-thumb bg-black object-contain"
    />
  )
}

function Deadline({ at }: { at: string }) {
  const { t, i18n } = useTranslation('job')
  const time = formatDateTime(at, i18n.language)
  const hours = Math.ceil((new Date(at).getTime() - Date.now()) / 3_600_000)
  return <p className="font-medium">{hours > 0 && hours <= 24 ? t('review.deadlineSoon', { hours, time }) : t('review.deadline', { time })}</p>
}

/** The preview gate of a video sequence: keep, redo or upgrade each clip against a server quote. */
export function ReviewPanel({ job }: { job: JobDetail }) {
  const { t, i18n } = useTranslation('job')
  const qc = useQueryClient()
  const toast = useToast()
  const me = useMe()
  const shots = draftShots(job)
  const original = (job.spec.shots as string[] | undefined) ?? []
  const [choice, setChoice] = useState<Record<number, Choice>>({})
  const [prompts, setPrompts] = useState<Record<number, string>>({})
  const [notice, setNotice] = useState<'priceChanged' | 'insufficient' | null>(null)

  const pick = (i: number) => choice[i] ?? 'keep'
  const selected = shots.filter((s) => pick(s.index) === 'upgrade').map((s) => s.index)
  const redo = shots.filter((s) => pick(s.index) === 'redo').map((s) => s.index)
  const overrides: Record<number, string> = {}
  for (const i of redo) {
    const p = prompts[i]?.trim()
    if (p && p !== original[i - 1]) overrides[i] = p
  }
  const decision: ReviewDecision = { selected_shots: selected, redo_shots: redo, redo_prompt_overrides: overrides }

  // Prompt edits do not change the price, so only the shot choices key the quote.
  const quote = useQuery({
    queryKey: [...keys.jobs.detail(job.biz_id), 'quote', selected.join(), redo.join()],
    queryFn: ({ signal }) => jobsApi.quoteReview(job.biz_id, { selected_shots: selected, redo_shots: redo, redo_prompt_overrides: {} }, signal),
    placeholderData: keepPreviousData,
  })

  const confirm = useMutation({
    mutationFn: () => jobsApi.resume(job.biz_id, { ...decision, quote_total: quote.data!.total }),
    onSuccess: () => {
      toast(t('review.submitted'))
      qc.invalidateQueries({ queryKey: keys.jobs.detail(job.biz_id) })
      qc.invalidateQueries({ queryKey: keys.jobs.all })
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === 'price_changed') {
        setNotice('priceChanged')
        quote.refetch()
      } else if (err instanceof ApiError && err.code === 'insufficient_credits') {
        setNotice('insufficient')
      } else {
        toast(errorText(t, err))
      }
    },
  })
  const busy = confirm.isPending
  const n = (v: number) => formatNumber(v, i18n.language)
  const credits = (v: number | undefined) => (v === undefined ? t('ui:unknownValue') : t('review.quote.credits', { n: n(v) }))
  const duration = typeof job.spec.duration_seconds === 'number' ? (job.spec.duration_seconds as number) : undefined

  return (
    <section aria-labelledby="review-title" className="flex flex-col gap-5">
      <div role="status" className="flex gap-3 rounded-card border border-warning bg-warning-soft p-4 text-body text-warning-fg">
        <CirclePause aria-hidden className="mt-0.5 size-5 shrink-0 text-warning" />
        <div className="flex flex-col gap-1">
          <h2 id="review-title" className="text-section font-semibold">
            {t('review.title')}
          </h2>
          <p>{t('review.intro')}</p>
          {job.review_deadline && <Deadline at={job.review_deadline} />}
          <p>{t('review.expiry')}</p>
        </div>
      </div>

      <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_360px]">
        <ul className="grid gap-4 [grid-template-columns:repeat(auto-fill,minmax(300px,1fr))]">
          {shots.map((s) => {
            const c = pick(s.index)
            return (
              <li key={s.index}>
                <Card padding="sm" className={cn('flex flex-col gap-3', c !== 'keep' && 'border-primary')}>
                  <div className="flex items-center justify-between">
                    <span className="text-body font-semibold text-fg">{t('review.clip', { n: s.index })}</span>
                    <span className="flex gap-1.5 text-badge font-medium text-fg-muted">
                      <span className="rounded-badge bg-surface-2 px-1.5 py-0.5">768P</span>
                      {duration && <span className="rounded-badge bg-surface-2 px-1.5 py-0.5">{t('info.seconds', { n: duration })}</span>}
                    </span>
                  </div>
                  <Clip assetId={s.assetId} label={t('review.clip', { n: s.index })} />
                  {original[s.index - 1] && (
                    <div>
                      <p className="text-caption text-fg-muted">{t('review.prompt')}</p>
                      <p className="line-clamp-3 text-body text-fg">{original[s.index - 1]}</p>
                    </div>
                  )}
                  <SegmentedControl<Choice>
                    fullWidth
                    label={t('review.choice.label', { n: s.index })}
                    value={c}
                    onChange={(v) => {
                      setNotice(null)
                      setChoice((cur) => ({ ...cur, [s.index]: v }))
                      if (v === 'redo' && prompts[s.index] === undefined) setPrompts((cur) => ({ ...cur, [s.index]: original[s.index - 1] ?? '' }))
                    }}
                    options={[
                      { value: 'keep', label: t('review.choice.keep'), disabled: busy },
                      { value: 'redo', label: t('review.choice.redo'), disabled: busy },
                      { value: 'upgrade', label: t('review.choice.upgrade'), disabled: busy },
                    ]}
                  />
                  {c === 'redo' && (
                    <Field label={t('review.redoPrompt')}>
                      <Textarea
                        value={prompts[s.index] ?? ''}
                        maxChars={VIDEO_PROMPT_MAX}
                        disabled={busy}
                        onChange={(e) => setPrompts((cur) => ({ ...cur, [s.index]: e.target.value }))}
                      />
                    </Field>
                  )}
                </Card>
              </li>
            )
          })}
        </ul>

        <aside aria-labelledby="quote-title" className="flex flex-col gap-4 xl:sticky xl:top-4">
          <Card padding="md" className="flex flex-col gap-3">
            <h3 id="quote-title" className="text-section font-semibold text-fg">
              {t('review.quote.title')}
            </h3>
            {quote.isError && !quote.data ? (
              <p className="text-body text-fg-muted">{t('review.quote.unknown')}</p>
            ) : (
              <dl className="flex flex-col gap-2 text-body">
                {(quote.data?.items ?? []).map((it) => (
                  <div key={it.kind} className="flex justify-between gap-3">
                    <dt className="text-fg-muted">
                      {['video_redo', 'video_upgrade', 'video_compose', 'prompt_enhance'].includes(it.kind)
                        ? t(`review.quote.item.${it.kind}`, { n: it.count })
                        : t('review.quote.item.other')}
                    </dt>
                    <dd className="text-right text-fg tabular-nums">{it.kind === 'video_compose' && it.credits === 0 ? t('review.quote.composeFree') : credits(it.credits)}</dd>
                  </div>
                ))}
                <div className="flex justify-between gap-3 border-t border-border pt-2 text-section font-semibold">
                  <dt>{t('review.quote.total')}</dt>
                  <dd className="tabular-nums">{credits(quote.data?.total)}</dd>
                </div>
                <div className="flex justify-between gap-3 text-caption">
                  <dt className="text-fg-muted">{t('review.quote.reserved')}</dt>
                  <dd className="tabular-nums">{job.credits.frozen === null ? t('ui:unknownValue') : credits(job.credits.frozen)}</dd>
                </div>
                <div className="flex justify-between gap-3 text-caption">
                  <dt className="text-fg-muted">{t('review.quote.balance')}</dt>
                  <dd className="tabular-nums">{credits(me.data?.balance)}</dd>
                </div>
              </dl>
            )}
            {quote.isFetching && <p className="text-caption text-fg-muted">{t('review.quote.updating')}</p>}
            <p className="text-caption text-fg-muted">{t('review.quote.note')}</p>
            {notice === 'priceChanged' && (
              <p role="alert" className="text-caption text-warning-fg">
                {t('review.priceChanged')}
              </p>
            )}
            {notice === 'insufficient' && (
              <p role="alert" className="flex flex-wrap items-center gap-2 text-caption text-danger-fg">
                {t('review.insufficient')}
                <Link to="/credits" className={buttonClasses('ghost', 'sm')}>
                  {t('review.topUp')}
                </Link>
              </p>
            )}
            <Button
              variant="primary"
              size="lg"
              loading={busy}
              disabled={!quote.data || quote.isFetching}
              onClick={() => {
                setNotice(null)
                confirm.mutate()
              }}
            >
              {t('review.confirm')}
            </Button>
          </Card>
          <Card padding="md" className="flex flex-col gap-2">
            <h3 className="text-body font-semibold text-fg">{t('review.quote.compare')}</h3>
            <div className="flex justify-between text-body">
              <span className="text-fg-muted">{t('review.quote.current')}</span>
              <span className="font-medium tabular-nums">{credits(quote.data?.total)}</span>
            </div>
            <div className="flex justify-between text-body">
              <span className="text-fg-muted">{t('review.quote.allUpgrade')}</span>
              <span className="font-medium tabular-nums">{credits(quote.data?.all_upgrade)}</span>
            </div>
          </Card>
        </aside>
      </div>
    </section>
  )
}
