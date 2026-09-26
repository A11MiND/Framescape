import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Clock, Coins, Gift, Info, ReceiptText, RotateCcw, ShoppingCart, Trophy, UserPlus } from 'lucide-react'
import { Button, Card, EmptyState, ErrorState, Kpi, PageHeader, Skeleton, cn } from '../../ui'
import { creditsApi, type LedgerEntry } from '../../lib/api/credits'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { formatDateTime, formatNumber } from '../../lib/format'
import { useMe } from '../../app/useMe'
import { useToast } from '../../components/Toast'

const DEMO_TOPUP = 200

type Kind = 'hold' | 'commit' | 'refund' | 'recharge' | 'grant' | 'reward'

function kindOf(e: LedgerEntry): Kind {
  if (e.direction === 'recharge') {
    if (e.remark.kind.startsWith('community_streak_')) return 'reward'
    if (e.remark.kind === 'recharge_custom') return 'grant'
    return 'recharge'
  }
  return (['hold', 'commit', 'refund'] as const).find((k) => k === e.direction) ?? 'recharge'
}

const ICONS: Record<Kind, typeof Coins> = { hold: Clock, commit: ShoppingCart, refund: RotateCcw, recharge: Gift, grant: UserPlus, reward: Trophy }

function remarkText(t: TFunction, e: LedgerEntry, kind: Kind) {
  const r = e.remark
  switch (kind) {
    case 'hold':
      return ['hold_job', 'hold_retry', 'hold_upgrade'].includes(r.kind) ? t(`remark.${r.kind}`) : t('remark.hold')
    case 'commit':
      return r.shortfall ? t('remark.commitShortfall', { n: r.shortfall }) : t('remark.commit')
    case 'refund':
      return t('remark.refund')
    case 'reward':
      return t('remark.streak', { days: r.kind.replace('community_streak_', '') })
    case 'grant':
      return r.text ? `${t('remark.recharge_custom')} · ${r.text}` : t('remark.recharge_custom')
    default:
      return r.kind === 'recharge_demo' ? t('remark.recharge_demo') : r.text || t('ledger.none')
  }
}

/** The event's own size: a charge or top-up moves available credits; a reserve or release moves them to or from reserved. */
function Amount({ e, kind, lang }: { e: LedgerEntry; kind: Kind; lang: string }) {
  const { t } = useTranslation('credits')
  if (kind === 'hold') return <span className="text-fg">{t('amount.hold', { n: formatNumber(e.remark.amount, lang) })}</span>
  if (kind === 'refund') return <span className="text-fg">{t('amount.refund', { n: formatNumber(e.remark.amount, lang) })}</span>
  const n = e.amount
  return <span className={cn('font-semibold', n > 0 ? 'text-success-fg' : 'text-fg')}>{`${n > 0 ? '+' : n < 0 ? '−' : ''}${formatNumber(Math.abs(n), lang)}`}</span>
}

function Related({ e }: { e: LedgerEntry }) {
  const { t } = useTranslation('credits')
  if (!e.job) return <span className="text-fg-muted">{t('ledger.none')}</span>
  return (
    <Link to={`/jobs/${e.job.biz_id}`} className="flex min-w-0 items-center gap-2 hover:underline">
      <span className="size-9 shrink-0 overflow-hidden rounded-thumb bg-surface-2">{e.job.cover_url && <img src={e.job.cover_url} alt="" className="size-full object-cover" />}</span>
      <span className="truncate text-body text-fg">{e.job.title || e.job.biz_id}</span>
    </Link>
  )
}

/** Credits (spec P18): available and reserved, how they move, and every ledger event with its task. */
export default function CreditsPage() {
  const { t, i18n } = useTranslation('credits')
  const lang = i18n.language
  const qc = useQueryClient()
  const toast = useToast()
  const me = useMe()
  const ledger = useInfiniteQuery({
    queryKey: [...keys.credits.all, 'ledger'],
    queryFn: ({ pageParam }) => creditsApi.ledger(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
  })
  const topup = useMutation({
    mutationFn: creditsApi.topup,
    onSuccess: (res) => {
      toast(t('topup.done', { n: res.credited }))
      qc.invalidateQueries({ queryKey: keys.me })
      qc.invalidateQueries({ queryKey: keys.credits.all })
    },
    onError: (err) => toast(errorText(t, err)),
  })
  const entries = ledger.data?.pages.flatMap((p) => p.entries) ?? []
  const admin = Boolean(me.data?.is_admin)

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6">
      <PageHeader title={t('title')} description={t('description')} />
      <div className="grid gap-4 lg:grid-cols-3">
        <Kpi icon={<Coins className="size-5" />} label={t('available')} value={me.data ? formatNumber(me.data.balance, lang) : '—'} />
        <Kpi
          icon={<Clock className="size-5" />}
          label={t('held')}
          value={me.data ? formatNumber(me.data.held, lang) : '—'}
          note={
            <>
              <span className="font-medium text-fg">{t('heldNote')}</span> · {t('heldHelp')}
            </>
          }
        />
        <Card className="flex flex-col gap-2">
          <h2 className="flex items-center gap-1.5 text-body font-semibold text-fg">
            <Info aria-hidden className="size-4 text-primary-text" />
            {t('about.title')}
          </h2>
          <ul className="flex list-disc flex-col gap-1 pl-5 text-caption text-fg-muted">
            <li>{t('about.reserve')}</li>
            <li>{t('about.settle')}</li>
            <li>{t('about.release')}</li>
            <li>{t('about.overage')}</li>
          </ul>
        </Card>
      </div>

      <Card className="flex flex-wrap items-center gap-3">
        <Gift aria-hidden className="size-5 text-primary-text" />
        <div className="min-w-0 flex-1">
          <p className="text-body font-semibold text-fg">{t('topup.title')}</p>
          <p className="text-caption text-fg-muted">{admin ? t('topup.note') : t('topup.unavailable')}</p>
        </div>
        {admin && (
          <Button variant="primary" loading={topup.isPending} onClick={() => topup.mutate()}>
            {t('topup.action', { n: DEMO_TOPUP })}
          </Button>
        )}
      </Card>

      <section aria-labelledby="ledger-title" className="flex flex-col gap-3">
        <h2 id="ledger-title" className="text-title font-semibold text-fg">
          {t('ledger.title')}
        </h2>
        <p role="note" className="flex items-start gap-2 rounded-card border border-warning bg-warning-soft px-3 py-2 text-caption text-warning-fg">
          <AlertTriangle aria-hidden className="mt-0.5 size-4 shrink-0" />
          {t('ledger.notSum')}
        </p>
        {ledger.isPending ? (
          <Skeleton className="h-64 w-full" />
        ) : ledger.isError ? (
          <ErrorState message={errorText(t, ledger.error)} onRetry={() => ledger.refetch()} />
        ) : entries.length === 0 ? (
          <Card padding="none">
            <EmptyState icon={<ReceiptText className="size-7" />} title={t('ledger.empty')} body={t('ledger.emptyBody')} />
          </Card>
        ) : (
          <>
            <div className="hidden overflow-x-auto rounded-card border border-border bg-surface md:block">
              <table className="w-full text-left">
                <thead>
                  <tr className="border-b border-border bg-surface-2 text-caption text-fg-muted">
                    <th scope="col" className="py-2.5 pr-3 pl-4 font-medium">{t('ledger.time')}</th>
                    <th scope="col" className="px-3 font-medium">{t('ledger.type')}</th>
                    <th scope="col" className="px-3 font-medium">{t('ledger.related')}</th>
                    <th scope="col" className="px-3 font-medium">{t('ledger.remark')}</th>
                    <th scope="col" className="py-2.5 pr-4 pl-3 text-right font-medium">{t('ledger.amount')}</th>
                  </tr>
                </thead>
                <tbody>
                  {entries.map((e, i) => {
                    const kind = kindOf(e)
                    const Icon = ICONS[kind]
                    return (
                      <tr key={`${e.created_at}-${i}`} className="border-b border-border last:border-0">
                        <td className="py-3 pr-3 pl-4 text-caption whitespace-nowrap text-fg-muted tabular-nums">{formatDateTime(e.created_at, lang)}</td>
                        <td className="px-3">
                          <span className="inline-flex items-center gap-1.5 text-body whitespace-nowrap text-fg">
                            <Icon aria-hidden className="size-4 text-fg-muted" />
                            {t(`type.${kind}`)}
                          </span>
                        </td>
                        <td className="max-w-64 px-3">
                          <Related e={e} />
                        </td>
                        <td className="px-3 text-caption text-fg-muted">
                          {remarkText(t, e, kind)}
                        </td>
                        <td className="py-3 pr-4 pl-3 text-right text-body whitespace-nowrap tabular-nums">
                          <Amount e={e} kind={kind} lang={lang} />
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
            <ul className="flex flex-col gap-2 md:hidden">
              {entries.map((e, i) => {
                const kind = kindOf(e)
                const Icon = ICONS[kind]
                return (
                  <li key={`${e.created_at}-${i}`} className="flex flex-col gap-2 rounded-card border border-border bg-surface p-3">
                    <div className="flex items-center justify-between gap-2">
                      <span className="inline-flex items-center gap-1.5 text-body font-medium text-fg">
                        <Icon aria-hidden className="size-4 text-fg-muted" />
                        {t(`type.${kind}`)}
                      </span>
                      <span className="text-body tabular-nums">
                        <Amount e={e} kind={kind} lang={lang} />
                      </span>
                    </div>
                    <Related e={e} />
                    <p className="text-caption text-fg-muted">{remarkText(t, e, kind)}</p>
                    <p className="text-caption text-fg-muted tabular-nums">{formatDateTime(e.created_at, lang)}</p>
                  </li>
                )
              })}
            </ul>
            {ledger.hasNextPage && (
              <div className="flex justify-center">
                <Button loading={ledger.isFetchingNextPage} onClick={() => ledger.fetchNextPage()}>
                  {t('ledger.loadMore')}
                </Button>
              </div>
            )}
          </>
        )}
      </section>
    </div>
  )
}
