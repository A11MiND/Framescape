import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, ChevronDown, Flame } from 'lucide-react'
import { Skeleton, cn } from '../../ui'
import { useStreak } from './helpers'

const DAYS = 84
const OPEN_KEY = 'aigc.community.streakOpen'

function utcDay(offset: number) {
  const d = new Date()
  d.setUTCDate(d.getUTCDate() - offset)
  return d.toISOString().slice(0, 10)
}

/** Monday-first weekday of a UTC date string, 0-6. */
function weekday(date: string) {
  return (new Date(`${date}T00:00:00Z`).getUTCDay() + 6) % 7
}

/** Publishing streak: 12-week heatmap, milestone rewards and their monthly caps (counted in times). */
export function StreakPanel({ id }: { id: string }) {
  const { t, i18n } = useTranslation('community')
  const streak = useStreak(true)
  const [open, setOpen] = useState(() => {
    try {
      return localStorage.getItem(OPEN_KEY) !== '0'
    } catch {
      return true
    }
  })
  const toggle = () => {
    setOpen(!open)
    try {
      localStorage.setItem(OPEN_KEY, open ? '0' : '1')
    } catch {
      // remembered for this visit only
    }
  }

  const published = new Set(streak.data?.published_dates ?? [])
  const days = Array.from({ length: DAYS }, (_, i) => utcDay(DAYS - 1 - i))
  const lead = weekday(days[0])
  const count = days.filter((d) => published.has(d)).length
  const fmt = new Intl.DateTimeFormat(i18n.language, { month: 'short', day: 'numeric', timeZone: 'UTC' })
  const weekdayName = new Intl.DateTimeFormat(i18n.language, { weekday: 'narrow', timeZone: 'UTC' })
  const current = streak.data?.current_streak ?? 0

  return (
    <section id={id} aria-labelledby={`${id}-title`} className="rounded-card border border-border bg-surface">
      <button type="button" aria-expanded={open} onClick={toggle} className="flex w-full items-center gap-3 px-4 py-3 text-left">
        <Flame aria-hidden className="size-5 text-warning" />
        <span className="flex-1">
          <span id={`${id}-title`} className="block text-body font-semibold text-fg">
            {t('streak.title')}
          </span>
          <span className="text-caption text-fg-muted">{t('streak.current', { n: current })}</span>
        </span>
        <ChevronDown aria-hidden className={cn('size-4 text-fg-muted transition-transform', open && 'rotate-180')} />
      </button>
      {open && (
        <div className="grid gap-6 border-t border-border p-4 xl:grid-cols-[auto_minmax(0,1fr)]">
          {streak.isPending ? (
            <Skeleton className="h-32 w-full xl:col-span-2" />
          ) : (
            <>
              <div className="flex flex-col gap-2">
                <h3 className="text-label font-medium text-fg">{t('streak.heatmap')}</h3>
                <div className="flex gap-2">
                <div aria-hidden className="grid gap-1 text-[10px] leading-[0.875rem] text-fg-muted" style={{ gridTemplateRows: 'repeat(7, 0.875rem)' }}>
                  {Array.from({ length: 7 }, (_, i) => (
                    <span key={i}>{i % 2 === 0 ? weekdayName.format(new Date(Date.UTC(2024, 0, 1 + i))) : ''}</span>
                  ))}
                </div>
                <div
                  role="img"
                  aria-label={`${t('streak.heatmap')}: ${t('streak.legendDone')} ${count}`}
                  className="grid w-fit grid-flow-col grid-rows-7 gap-1"
                  style={{ gridTemplateRows: 'repeat(7, 0.875rem)' }}
                >
                  {Array.from({ length: lead }, (_, i) => (
                    <span key={`pad-${i}`} />
                  ))}
                  {days.map((d) => (
                    <span
                      key={d}
                      title={t('streak.day', { date: fmt.format(new Date(`${d}T00:00:00Z`)), state: published.has(d) ? t('streak.legendDone') : t('streak.legendNone') })}
                      className={cn('size-3.5 rounded-[3px]', published.has(d) ? 'bg-primary' : 'border border-border bg-surface-2')}
                    />
                  ))}
                </div>
                </div>
                <div className="flex items-center gap-3 text-caption text-fg-muted">
                  <span className="inline-flex items-center gap-1">
                    <span aria-hidden className="size-3 rounded-[3px] border border-border bg-surface-2" />
                    {t('streak.legendNone')}
                  </span>
                  <span className="inline-flex items-center gap-1">
                    <span aria-hidden className="size-3 rounded-[3px] bg-primary" />
                    {t('streak.legendDone')}
                  </span>
                </div>
                <p className="max-w-sm text-caption text-fg-muted">{t('streak.rule')}</p>
              </div>
              <div className="flex flex-col gap-2">
                <h3 className="text-label font-medium text-fg">{t('streak.milestones')}</h3>
                <ul className="grid gap-2 sm:grid-cols-3">
                  {(streak.data?.milestones ?? []).map((m) => {
                    const reached = current >= m.days
                    return (
                      <li key={m.days} className={cn('flex flex-col gap-1 rounded-card border p-3', reached ? 'border-primary bg-primary-soft' : 'border-border')}>
                        <span className="flex items-center gap-1.5 text-body font-semibold text-fg">
                          {reached && <Check aria-label={t('streak.reached')} className="size-4 text-primary-text" />}
                          {t('streak.milestone', { days: m.days })}
                        </span>
                        <span className="text-body text-fg tabular-nums">{t('streak.reward', { n: m.credits })}</span>
                        <span className="text-caption text-fg-muted tabular-nums">{m.monthly_cap > 0 ? t('streak.cap', { used: m.used_this_month, cap: m.monthly_cap }) : t('streak.uncapped')}</span>
                      </li>
                    )
                  })}
                </ul>
                <p className="text-caption text-fg-muted">{t('streak.capNote')}</p>
              </div>
            </>
          )}
        </div>
      )}
    </section>
  )
}
