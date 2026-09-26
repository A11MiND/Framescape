import { useEffect, useState } from 'react'
import { Link, useLocation, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { CircleCheck, CirclePause, CircleX, ListChecks, LoaderCircle, Search, SlidersHorizontal, type LucideIcon } from 'lucide-react'
import { Button, Drawer, EmptyState, ErrorState, Input, PageHeader, Select, Skeleton, buttonClasses, cn } from '../../ui'
import { jobsApi } from '../../lib/api/jobs'
import { projectsApi } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { formatNumber } from '../../lib/format'
import { useDebouncedValue } from '../../hooks/useDebouncedValue'
import { BUCKETS, OTHER_STATUSES, WORKFLOWS, isFiltered, listFilter, pinsNeedsReview, readView, writeView, type TaskView } from './filters'
import { TaskList } from './TaskItem'

const CARD_STYLE: Record<(typeof BUCKETS)[number], { icon: LucideIcon; tone: string }> = {
  needs_review: { icon: CirclePause, tone: 'text-warning' },
  active: { icon: LoaderCircle, tone: 'text-primary-text' },
  succeeded: { icon: CircleCheck, tone: 'text-success' },
  failed: { icon: CircleX, tone: 'text-danger-icon' },
}

function ListSkeleton() {
  return (
    <div className="flex flex-col gap-2" aria-busy="true">
      {Array.from({ length: 5 }, (_, i) => (
        <Skeleton key={i} className="h-16 w-full" />
      ))}
    </div>
  )
}

export default function TaskCenter() {
  const { t, i18n } = useTranslation('tasks')
  const [params, setParams] = useSearchParams()
  const location = useLocation()
  const view = readView(params)
  const highlight = (location.state as { highlightBizId?: string } | null)?.highlightBizId
  const setView = (next: Partial<TaskView>) => setParams(writeView({ ...view, ...next }), { replace: true })

  const [search, setSearch] = useState(view.q ?? '')
  const debounced = useDebouncedValue(search.trim(), 300)
  useEffect(() => {
    if ((view.q ?? '') !== debounced) setView({ q: debounced || undefined })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debounced])

  const counts = useQuery({ queryKey: keys.jobs.summary(view.project), queryFn: () => jobsApi.summary(view.project) })
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list })
  const filter = listFilter(view)
  const list = useInfiniteQuery({
    queryKey: keys.jobs.list({ ...filter }),
    queryFn: ({ pageParam }) => jobsApi.list({ ...filter, cursor: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
  })
  const pinned = useQuery({
    queryKey: keys.jobs.list({ bucket: 'needs_review', project_id: view.project, limit: 20 }),
    queryFn: () => jobsApi.list({ bucket: 'needs_review', project_id: view.project, limit: 20 }),
    enabled: pinsNeedsReview(view),
  })

  const n = (v: number | undefined) => (counts.isError || v === undefined ? t('ui:unknownValue') : formatNumber(v, i18n.language))
  const jobs = list.data?.pages.flatMap((p) => p.jobs) ?? []
  const pinnedJobs = pinsNeedsReview(view) ? (pinned.data?.jobs ?? []) : []
  const statusValue = view.bucket ?? view.status ?? ''
  const [sheet, setSheet] = useState(false)
  const activeFilters = [statusValue, view.project, view.workflow, view.oldest ? 'o' : ''].filter(Boolean).length
  const filterControls = (
    <>
      <Select
        aria-label={t('filter.label')}
        value={statusValue}
        onChange={(e) => {
          const v = e.target.value
          setView({ bucket: (BUCKETS as readonly string[]).includes(v) ? v : undefined, status: (OTHER_STATUSES as readonly string[]).includes(v) ? v : undefined })
        }}
      >
        <option value="">{t('filter.all')}</option>
        <optgroup label={t('filter.groups')}>
          {BUCKETS.map((b) => (
            <option key={b} value={b}>
              {t('filter.withCount', { label: b === 'failed' ? t('filter.failedHint') : t(`cards.${b}`), n: n(counts.data?.[b]) })}
            </option>
          ))}
        </optgroup>
        <optgroup label={t('filter.other')}>
          {OTHER_STATUSES.map((s) => (
            <option key={s} value={s}>
              {t('filter.withCount', { label: t(`ui:status.${s}`), n: n(counts.data?.statuses?.[s]) })}
            </option>
          ))}
        </optgroup>
      </Select>
      <Select aria-label={t('filter.project')} value={view.project ?? ''} onChange={(e) => setView({ project: e.target.value || undefined })} className="max-w-[200px]">
        <option value="">{t('filter.allProjects')}</option>
        {(projects.data?.projects ?? []).map((p) => (
          <option key={p.biz_id} value={p.biz_id}>
            {p.name}
          </option>
        ))}
      </Select>
      <Select aria-label={t('filter.type')} value={view.workflow ?? ''} onChange={(e) => setView({ workflow: e.target.value || undefined })}>
        <option value="">{t('filter.allTypes')}</option>
        {WORKFLOWS.map((w) => (
          <option key={w} value={w}>
            {t(`type.${w}`)}
          </option>
        ))}
      </Select>
      <Select aria-label={t('filter.order')} value={view.oldest ? 'oldest' : ''} onChange={(e) => setView({ oldest: e.target.value === 'oldest' })}>
        <option value="">{t('filter.newest')}</option>
        <option value="oldest">{t('filter.oldest')}</option>
      </Select>
    </>
  )

  // Wide screens put the search beside the status cards; narrower ones keep it with the filters.
  const searchBox = (className: string) => (
    <div className={cn('relative', className)}>
      <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-fg-muted" />
      <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t('filter.search')} aria-label={t('filter.search')} className="pl-9" />
    </div>
  )

  return (
    <div className="mx-auto flex max-w-[1280px] flex-col gap-5 px-4 py-6 lg:px-6">
      <PageHeader title={t('title')} description={t('description')} />

      <div className="-mx-4 flex snap-x gap-3 overflow-x-auto px-4 pb-1 md:mx-0 md:grid md:grid-cols-4 md:overflow-visible md:px-0 md:pb-0 xl:grid-cols-[repeat(4,minmax(0,1fr))_minmax(280px,1.4fr)]">
        {BUCKETS.map((b) => {
          const style = CARD_STYLE[b]
          const current = view.bucket === b
          return (
            <button
              key={b}
              type="button"
              aria-pressed={current}
              onClick={() => setView({ bucket: current ? undefined : b, status: undefined })}
              className={cn(
                'flex min-w-[168px] shrink-0 snap-start items-center gap-3 rounded-card border bg-surface p-3.5 text-left transition-colors md:min-w-0',
                current ? 'border-2 border-primary bg-primary-soft p-[13px]' : 'border-border hover:bg-surface-2',
              )}
            >
              <style.icon aria-hidden className={cn('size-6 shrink-0', style.tone)} />
              <span className="min-w-0 flex-1 truncate text-body font-medium text-fg">{t(`cards.${b}`)}</span>
              <span className="text-section font-semibold text-fg tabular-nums">{n(counts.data?.[b])}</span>
            </button>
          )
        })}
        {searchBox('hidden self-center xl:block')}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {searchBox('min-w-[220px] flex-1 xl:hidden')}
        <div className="hidden flex-wrap items-center gap-2 md:flex">{filterControls}</div>
        <Button className="md:hidden" icon={<SlidersHorizontal aria-hidden className="size-4" />} onClick={() => setSheet(true)}>
          {activeFilters ? t('filter.buttonCount', { n: activeFilters }) : t('filter.button')}
        </Button>
      </div>

      <Drawer open={sheet} onOpenChange={setSheet} title={t('filter.sheetTitle')}>
        <div className="flex flex-col gap-3 [&_select]:w-full [&_select]:max-w-none">{filterControls}</div>
      </Drawer>

      {pinnedJobs.length > 0 && (
        <section aria-labelledby="pinned-title" className="flex flex-col gap-2">
          <div>
            <h2 id="pinned-title" className="text-section font-semibold text-fg">
              {t('pinned')}
            </h2>
            <p className="text-caption text-fg-muted">{t('pinnedHint')}</p>
          </div>
          <TaskList jobs={pinnedJobs} highlight={highlight} />
        </section>
      )}

      {list.isPending ? (
        <ListSkeleton />
      ) : list.isError ? (
        <ErrorState message={errorText(t, list.error)} onRetry={() => list.refetch()} />
      ) : jobs.length === 0 && pinnedJobs.length === 0 ? (
        isFiltered(view) ? (
          <EmptyState
            icon={<ListChecks className="size-7" />}
            title={t('empty.filtered')}
            action={
              <Button
                onClick={() => {
                  setSearch('')
                  setParams(new URLSearchParams(), { replace: true })
                }}
              >
                {t('empty.clear')}
              </Button>
            }
          />
        ) : (
          <EmptyState
            icon={<ListChecks className="size-7" />}
            title={t('empty.title')}
            body={t('empty.body')}
            action={
              <Link to="/" className={buttonClasses('primary')}>
                {t('empty.action')}
              </Link>
            }
          />
        )
      ) : (
        <section className="flex flex-col gap-3">
          {jobs.length > 0 && <TaskList jobs={jobs} highlight={highlight} />}
          {list.hasNextPage && (
            <div className="flex justify-center">
              <Button loading={list.isFetchingNextPage} onClick={() => list.fetchNextPage()}>
                {t('action.loadMore')}
              </Button>
            </div>
          )}
        </section>
      )}
    </div>
  )
}
