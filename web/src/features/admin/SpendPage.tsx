import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { Card, EmptyState, ErrorState, Kpi, PageHeader, SegmentedControl, Skeleton } from '../../ui'
import { errorText } from '../../lib/errorText'
import { formatNumber } from '../../lib/format'

export default function SpendPage() {
  const { t, i18n } = useTranslation('adminV2')
  const [range, setRange] = useState('7')
  const overview = useQuery({ queryKey: ['admin', 'overview'], queryFn: api.adminOverview })
  const usage = useQuery({ queryKey: ['admin', 'usage', range], queryFn: () => api.adminUsage(Number(range)) })
  const money = (n: number) => new Intl.NumberFormat(i18n.language, { style: 'currency', currency: 'CNY' }).format(n)
  const number = (n: number) => formatNumber(n, i18n.language)
  const days = [...(usage.data?.days ?? [])].sort((a, b) => a.day.localeCompare(b.day))
  const max = Math.max(1, ...days.map((d) => d.cost_yuan))
  const points = days.map((d, i) => `${24 + (i * 552) / Math.max(1, days.length - 1)},${180 - (d.cost_yuan / max) * 150}`).join(' ')
  const o = overview.data
  const total = Object.values(o?.jobs_by_status ?? {}).reduce((a, b) => a + b, 0)
  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-6 px-4 py-6 lg:px-6">
      <PageHeader
        title={t('spend.title')}
        description={t('spend.description')}
        actions={
          <SegmentedControl
            label={t('spend.range')}
            value={range}
            onChange={setRange}
            options={['7', '30'].map((v) => ({ value: v, label: t('spend.days', { n: v }) }))}
          />
        }
      />
      {overview.isPending ? (
        <Skeleton className="h-28" />
      ) : overview.isError ? (
        <ErrorState message={errorText(t, overview.error)} onRetry={() => overview.refetch()} />
      ) : (
        o && (
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
            <Kpi label={t('spend.cost')} value={money(o.total_cost_yuan)} note={t('spend.allTime')} />
            <Kpi label={t('spend.consumed')} value={number(o.credits_consumed)} note={t('spend.allTime')} />
            <Kpi label={t('spend.recharged')} value={number(o.credits_recharged)} note={t('spend.allTime')} />
            <Kpi label={t('spend.jobs')} value={number(total)} note={t('spend.allTime')} />
            <Kpi label={t('spend.users')} value={number(o.user_count)} note={t('spend.allTime')} />
          </div>
        )
      )}
      <div className="grid items-start gap-5 xl:grid-cols-[2fr_1fr]">
        <Card>
          <h2 className="text-section font-semibold">{t('spend.trend')}</h2>
          <p className="text-caption text-fg-muted">{t('spend.rangeHelp')}</p>
          {usage.isPending ? (
            <Skeleton className="mt-4 h-56" />
          ) : usage.isError ? (
            <ErrorState message={errorText(t, usage.error)} onRetry={() => usage.refetch()} />
          ) : !days.length ? (
            <EmptyState title={t('spend.empty')} />
          ) : (
            <figure className="mt-4">
              <svg viewBox="0 0 600 210" role="img" aria-label={t('spend.chartAlt', { n: range })} className="w-full text-primary">
                <line x1="24" y1="180" x2="576" y2="180" stroke="var(--color-border)" />
                <polygon points={`24,180 ${points} ${days.length === 1 ? 24 : 576},180`} fill="currentColor" opacity="0.1" />
                <polyline points={points} fill="none" stroke="currentColor" strokeWidth="3" strokeLinejoin="round" />
                {days.map((d, i) => (
                  <circle
                    key={d.day}
                    cx={24 + (i * 552) / Math.max(1, days.length - 1)}
                    cy={180 - (d.cost_yuan / max) * 150}
                    r="3"
                    fill="currentColor"
                  >
                    <title>{`${d.day}: ${money(d.cost_yuan)}`}</title>
                  </circle>
                ))}
              </svg>
              <figcaption className="flex flex-wrap justify-between gap-2 text-caption text-fg-muted">
                <span>
                  {days[0].day} — {days[days.length - 1].day}
                </span>
                <span>
                  {t('spend.latest')} {money(days[days.length - 1].cost_yuan)}
                </span>
              </figcaption>
            </figure>
          )}
        </Card>
        <Card>
          <h2 className="mb-4 text-section font-semibold">{t('spend.statuses')}</h2>
          {overview.isPending ? (
            <Skeleton className="h-36" />
          ) : overview.isError ? (
            <p>{t('spend.unavailable')}</p>
          ) : !total ? (
            <EmptyState title={t('spend.noJobs')} />
          ) : (
            <dl className="space-y-3">
              {Object.entries(o?.jobs_by_status ?? {}).map(([s, n]) => (
                <div key={s} className="flex justify-between gap-3">
                  <dt className="text-body text-fg-muted">{t(`ui:status.${s}`, { defaultValue: t('spend.other') })}</dt>
                  <dd className="font-medium tabular-nums">{number(n)}</dd>
                </div>
              ))}
            </dl>
          )}
        </Card>
      </div>
      <Card className="overflow-hidden">
        <h2 className="mb-4 text-section font-semibold">{t('spend.detail')}</h2>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-body">
            <caption className="sr-only">{t('spend.detail')}</caption>
            <thead className="text-caption text-fg-muted">
              <tr>
                {['day', 'jobs', 'consumed', 'cost'].map((k) => (
                  <th key={k} scope="col" className="px-3 py-3">
                    {t(`spend.${k}`)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {[...days].reverse().map((d) => (
                <tr key={d.day} className="border-t border-border">
                  <td className="whitespace-nowrap px-3 py-3">{d.day}</td>
                  <td className="px-3 py-3 tabular-nums">{number(d.jobs)}</td>
                  <td className="px-3 py-3 tabular-nums">{number(d.credits_consumed)}</td>
                  <td className="px-3 py-3 tabular-nums">{money(d.cost_yuan)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="mt-4 text-caption text-fg-muted">{t('spend.source')}</p>
      </Card>
    </div>
  )
}
