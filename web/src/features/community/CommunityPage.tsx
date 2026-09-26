import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useInfiniteQuery, useMutation, useQueryClient, type InfiniteData } from '@tanstack/react-query'
import { Flame, Heart, ImageIcon, Play, Users, Video, X } from 'lucide-react'
import { Button, Card, Drawer, EmptyState, ErrorState, IconButton, PageHeader, SegmentedControl, Skeleton, TabList, Tabs, buttonClasses, cn } from '../../ui'
import { communityApi, type CommunityWork, type FeedFilter } from '../../lib/api/community'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { formatClipLength } from '../../lib/format'
import { useMediaQuery } from '../../hooks/useMediaQuery'
import { useAuthStore } from '../../lib/authStore'
import { useToast } from '../../components/Toast'
import { StreakPanel } from './StreakPanel'
import { WorkViewer } from './WorkViewer'
import { useStreak, workTitle } from './helpers'

type Page = { assets: CommunityWork[]; next_cursor?: string }

function WorkCard({ work, active, onOpen }: { work: CommunityWork; active: boolean; onOpen: () => void }) {
  const { t } = useTranslation('community')
  const video = work.type === 'video'
  const ratio = work.width && work.height ? `${work.width} / ${work.height}` : video ? '16 / 9' : '1 / 1'
  return (
    <li className="mb-3 break-inside-avoid">
      <button
        type="button"
        onClick={onOpen}
        aria-label={t('open', { title: workTitle(t, work) })}
        className={cn('relative block w-full overflow-hidden rounded-card border bg-surface-2', active ? 'border-primary ring-2 ring-primary' : 'border-border')}
        style={{ aspectRatio: ratio }}
      >
        {video && !work.thumb_url ? (
          <video src={work.public_url} muted preload="metadata" className="size-full object-cover" />
        ) : (
          <img src={work.thumb_url || work.public_url} alt="" loading="lazy" className="size-full object-cover" />
        )}
        <span aria-hidden className="absolute top-2 left-2 inline-flex size-7 items-center justify-center rounded-thumb bg-black/55 text-white">
          {video ? <Video className="size-4" /> : <ImageIcon className="size-4" />}
        </span>
        {video && (
          <span aria-hidden className="absolute inset-0 flex items-center justify-center">
            <span className="inline-flex size-9 items-center justify-center rounded-full bg-black/55 text-white">
              <Play className="size-4" />
            </span>
          </span>
        )}
        <span className="absolute inset-x-2 bottom-2 flex items-center justify-between text-badge font-medium text-white">
          {video && work.duration_ms ? <span className="rounded-badge bg-black/65 px-1.5 py-0.5 tabular-nums">{formatClipLength(work.duration_ms)}</span> : <span />}
          <span className="inline-flex items-center gap-1 rounded-badge bg-black/65 px-1.5 py-0.5 tabular-nums">
            <Heart aria-hidden className={cn('size-3', work.liked && 'fill-current')} />
            {work.like_count}
            <span className="sr-only">{t('likes', { n: work.like_count })}</span>
          </span>
        </span>
      </button>
    </li>
  )
}

/** Community (spec P17): works first, their public prompts, likes, and the publishing streak. */
export default function CommunityPage() {
  const { t } = useTranslation('community')
  const qc = useQueryClient()
  const toast = useToast()
  const signedIn = useAuthStore((s) => Boolean(s.accessToken))
  const wide = useMediaQuery('(min-width: 1280px)')
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') === 'mine' ? 'mine' : 'all'
  const type = params.get('type') === 'image' || params.get('type') === 'video' ? (params.get('type') as 'image' | 'video') : undefined
  const setParam = (key: string, value: string | undefined) => {
    const next = new URLSearchParams(params)
    if (value) next.set(key, value)
    else next.delete(key)
    setParams(next, { replace: true })
  }
  const filter: FeedFilter = { type, mine: tab === 'mine' }
  const feedKey = ['community', 'feed', filter] as const
  const needsLogin = tab === 'mine' && !signedIn
  const feed = useInfiniteQuery({
    queryKey: feedKey,
    queryFn: ({ pageParam }) => communityApi.feed(filter, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
    enabled: !needsLogin,
  })
  const streak = useStreak(signedIn)
  const works = feed.data?.pages.flatMap((p) => p.assets) ?? []
  const [openId, setOpenId] = useState<string | null>(null)
  const open = works.find((w) => w.biz_id === openId) ?? null

  const patchWork = (id: string, patch: Partial<CommunityWork> | null) =>
    qc.setQueriesData<InfiniteData<Page>>({ queryKey: ['community', 'feed'] }, (data) =>
      data
        ? {
            ...data,
            pages: data.pages.map((p) => ({
              ...p,
              assets: patch === null ? p.assets.filter((w) => w.biz_id !== id) : p.assets.map((w) => (w.biz_id === id ? { ...w, ...patch } : w)),
            })),
          }
        : data,
    )
  const like = useMutation({
    mutationFn: (w: CommunityWork) => (w.liked ? communityApi.unlike(w.biz_id) : communityApi.like(w.biz_id)),
    onSuccess: (res, w) => patchWork(w.biz_id, { liked: res.liked, like_count: res.like_count }),
    onError: (err) => toast(errorText(t, err)),
  })
  const unpublish = useMutation({
    mutationFn: (w: CommunityWork) => communityApi.unpublish(w.biz_id),
    onSuccess: (_, w) => {
      patchWork(w.biz_id, null)
      setOpenId(null)
      toast(t('viewer.unpublished'))
      qc.invalidateQueries({ queryKey: keys.assets.all })
    },
    onError: (err) => toast(errorText(t, err)),
  })
  const onLike = (w: CommunityWork) => {
    if (!signedIn) {
      toast(t('guest.like'))
      return
    }
    like.mutate(w)
  }

  const viewer = open && (
    <WorkViewer
      work={open}
      signedIn={signedIn}
      liking={like.isPending && like.variables?.biz_id === open.biz_id}
      unpublishing={unpublish.isPending}
      onLike={() => onLike(open)}
      onUnpublish={() => unpublish.mutate(open)}
    />
  )

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6">
      <PageHeader
        title={t('title')}
        description={t('description')}
        actions={
          signedIn && streak.data ? (
            <a href="#streak" className={buttonClasses('secondary')}>
              <Flame aria-hidden className="size-4 text-warning" />
              {t('streakEntry', { n: streak.data.current_streak })}
            </a>
          ) : undefined
        }
      />
      <Tabs value={tab} onValueChange={(v) => setParam('tab', v === 'mine' ? 'mine' : undefined)}>
        <TabList
          label={t('tab.label')}
          items={[
            { value: 'all', label: t('tab.all') },
            { value: 'mine', label: t('tab.mine') },
          ]}
        />
      </Tabs>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <SegmentedControl
          label={t('type.label')}
          value={type ?? 'all'}
          onChange={(v) => setParam('type', v === 'all' ? undefined : v)}
          options={[
            { value: 'all', label: t('type.all') },
            { value: 'image', label: t('type.image') },
            { value: 'video', label: t('type.video') },
          ]}
        />
        <span className="text-caption text-fg-muted">{t('sort')}</span>
      </div>

      <div className={cn('grid items-start gap-5', open && wide && 'xl:grid-cols-[minmax(0,1fr)_400px]')}>
        {needsLogin ? (
          <Card padding="none">
            <EmptyState
              icon={<Users className="size-7" />}
              title={t('guest.mine')}
              action={
                <Link to="/login" className={buttonClasses('primary')}>
                  {t('guest.login')}
                </Link>
              }
            />
          </Card>
        ) : feed.isPending ? (
          <div className="columns-2 gap-3 md:columns-3 2xl:columns-4">
            {Array.from({ length: 8 }, (_, i) => (
              <Skeleton key={i} className={cn('mb-3 w-full', i % 3 ? 'h-48' : 'h-64')} />
            ))}
          </div>
        ) : feed.isError ? (
          <ErrorState message={errorText(t, feed.error)} onRetry={() => feed.refetch()} />
        ) : works.length === 0 ? (
          <Card padding="none">
            {type ? (
              <EmptyState icon={<ImageIcon className="size-7" />} title={t('empty.filtered')} />
            ) : tab === 'mine' ? (
              <EmptyState
                icon={<Users className="size-7" />}
                title={t('empty.mine')}
                body={t('empty.mineBody')}
                action={
                  <Link to="/assets" className={buttonClasses('primary')}>
                    {t('empty.openLibrary')}
                  </Link>
                }
              />
            ) : (
              <EmptyState icon={<Users className="size-7" />} title={t('empty.all')} body={t('empty.allBody')} />
            )}
          </Card>
        ) : (
          <div className="flex flex-col gap-4">
            <ul className={cn('columns-2 gap-3 md:columns-3', open && wide ? '2xl:columns-3' : '2xl:columns-4')}>
              {works.map((w) => (
                <WorkCard key={w.biz_id} work={w} active={w.biz_id === openId} onOpen={() => setOpenId(w.biz_id)} />
              ))}
            </ul>
            {feed.hasNextPage && (
              <div className="flex justify-center">
                <Button loading={feed.isFetchingNextPage} onClick={() => feed.fetchNextPage()}>
                  {t('loadMore')}
                </Button>
              </div>
            )}
          </div>
        )}
        {open && wide && (
          <aside aria-label={t('viewer.label')} className="sticky top-4 max-h-[calc(100dvh-6rem)] overflow-y-auto rounded-card border border-border bg-surface p-4">
            <div className="mb-3 flex justify-end">
              <IconButton size="sm" label={t('viewer.close')} icon={<X className="size-4" />} onClick={() => setOpenId(null)} />
            </div>
            {viewer}
          </aside>
        )}
      </div>
      {!wide && (
        <Drawer open={open !== null} onOpenChange={(o) => !o && setOpenId(null)} title={t('viewer.label')}>
          {viewer}
        </Drawer>
      )}

      {signedIn && <StreakPanel id="streak" />}
    </div>
  )
}
