import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, type AssetResponse, type CommunityAsset, type CommunityStreak } from '../lib/api'
import { useAuthStore } from '../lib/authStore'
import AppShell from '../components/AppShell'

type Tab = 'feed' | 'mine'

// Plain stroke heart, filled when liked — same reasoning as every other
// icon in this codebase (no emoji-range codepoints, see CLAUDE.md's own
// icon-glyph convention).
function HeartIcon({ filled, className }: { filled: boolean; className?: string }) {
  return (
    <svg
      viewBox="0 0 20 20"
      fill={filled ? 'currentColor' : 'none'}
      stroke="currentColor"
      strokeWidth={1.6}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
    >
      <path d="M10 17.2s-6.8-4.1-8.6-8.2C.4 6 1.9 3 5 3c2 0 3.6 1.2 5 3.2C11.4 4.2 13 3 15 3c3.1 0 4.6 3 3.6 6-1.8 4.1-8.6 8.2-8.6 8.2z" />
    </svg>
  )
}

// §07's "社區功能：看別人做的作品，也可以發佈出去" ask — a masonry feed of
// every published asset across every account (handleCommunityFeed's own
// doc), same visual language as Assets.tsx's library grid but deliberately
// its own page rather than a mode of that one: nothing here is the current
// user's own to delete/reassign/download-in-bulk, so none of that surface
// belongs on the "全部作品" cards. Clicking one of those opens an inline
// lightbox instead of navigating to /assets/:id — that route is strictly
// "WHERE user_id = caller" (handleGetAsset's own doc, kept that way on
// purpose), so a community item, owned by someone else, was never going to
// be viewable there. The "我发布的" tab is the opposite case — every card
// there IS the caller's own asset, so it links straight to /assets/:id
// (full management already lives there) and additionally gets its own
// inline "撤销发布" button for the common one-off action, found missing
// live: there was no way to see or manage your own published set without
// remembering which individual assets you'd published and visiting each
// one's own detail page.
export default function Community() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const isGuest = !useAuthStore((s) => s.accessToken)
  const [tab, setTab] = useState<Tab>('feed')
  const feed = useQuery({ queryKey: ['community', 'feed'], queryFn: () => api.listCommunityFeed(90), enabled: tab === 'feed' })
  const mine = useQuery({
    queryKey: ['community', 'mine'],
    queryFn: () => api.listAssets({ isPublic: true, limit: 90 }),
    enabled: !isGuest && tab === 'mine',
  })
  // Both account-scoped, so neither means anything for a guest browsing
  // in off the login page's link — handleCommunityStreak/handleListAssets
  // (with is_public) both still sit behind requireAuth() server-side too,
  // this is just the frontend not firing a call that would 401 anyway.
  const streak = useQuery({ queryKey: ['community', 'streak'], queryFn: () => api.getCommunityStreak(), enabled: !isGuest })
  const [active, setActive] = useState<CommunityAsset | null>(null)

  const unpublish = useMutation({
    mutationFn: (bizId: string) => api.setAssetPublic(bizId, false),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['community', 'mine'] })
      qc.invalidateQueries({ queryKey: ['community', 'feed'] })
    },
  })

  // Toggling happens from the lightbox, not the grid card — the card is
  // itself a <button> (opens the lightbox), and a like button nested inside
  // it would be an invalid button-in-button. `active` is a plain snapshot
  // taken at click time (not wired to the feed query), so it's updated here
  // directly for an immediately-responsive count/heart in the open lightbox;
  // the feed query is invalidated too so leaving and reopening it, or the
  // grid's own like-count badge, stay correct as well.
  const toggleLike = useMutation({
    mutationFn: (a: CommunityAsset) => (a.liked ? api.unlikeAsset(a.biz_id) : api.likeAsset(a.biz_id)),
    onSuccess: (res) => {
      setActive((cur) => (cur ? { ...cur, liked: res.liked, like_count: res.like_count } : cur))
      qc.invalidateQueries({ queryKey: ['community', 'feed'] })
    },
  })

  return (
    <AppShell>
      <div className="mx-auto max-w-5xl px-6 py-8">
        <div className="mb-2">
          <h1 className="text-lg font-medium">{t('community.title')}</h1>
          <p className="mt-1 text-sm text-zinc-500">{t('community.subtitle')}</p>
        </div>

        {streak.data && <StreakPanel streak={streak.data} />}

        {!isGuest && (
          <div className="mb-4 flex gap-1 rounded-lg border border-zinc-800 bg-zinc-900 p-1 text-sm w-fit">
            {(['feed', 'mine'] as const).map((tb) => (
              <button
                key={tb}
                onClick={() => setTab(tb)}
                className={`rounded-md px-3 py-1.5 transition ${
                  tab === tb ? 'bg-violet-500 text-white' : 'text-zinc-400 hover:text-zinc-200'
                }`}
              >
                {t(`community.tabs.${tb}`)}
              </button>
            ))}
          </div>
        )}

        {(isGuest || tab === 'feed') && (
          <>
            {feed.isSuccess && feed.data.assets.length === 0 && <p className="mt-6 text-zinc-500">{t('community.empty')}</p>}
            <div className="columns-2 gap-3 sm:columns-3 lg:columns-4 [&>*]:mb-3">
              {feed.data?.assets.map((a) => (
                <button
                  key={a.biz_id}
                  onClick={() => setActive(a)}
                  className="group relative block w-full overflow-hidden rounded-xl border border-zinc-800 bg-zinc-900 text-left transition hover:border-zinc-700"
                >
                  {a.type === 'video' ? (
                    <video src={a.public_url} muted className="block max-h-96 w-full bg-black object-contain" />
                  ) : (
                    <img src={a.public_url} alt="" loading="lazy" className="block max-h-96 w-full object-cover" />
                  )}
                  {a.resolution_tag && (
                    <span className="absolute bottom-1.5 left-1.5 rounded bg-violet-500/80 px-1.5 py-0.5 font-mono text-[10px] font-medium text-white">
                      {a.resolution_tag}
                    </span>
                  )}
                  {/* Toggling lives in the lightbox (this card is itself a
                      <button>, so a nested like button would be invalid
                      HTML) — this is just the count, same fixed-on-fixed-bg
                      neutral-* pairing as the lightbox below, since it sits
                      on the media itself, not this card's own themed
                      background. */}
                  {a.like_count > 0 && (
                    <span className="absolute bottom-1.5 right-1.5 flex items-center gap-1 rounded bg-black/60 px-1.5 py-0.5 text-[10px] font-medium text-neutral-200">
                      <HeartIcon filled={a.liked} className="h-3 w-3" />
                      {a.like_count}
                    </span>
                  )}
                  {/* Previously only shown after clicking through to the
                      lightbox — the prompt is the whole point of "看别人做
                      的作品", found live off "community 不展示提示词". */}
                  {a.prompt && <p className="line-clamp-2 px-2 py-1.5 text-xs text-zinc-400">{a.prompt}</p>}
                </button>
              ))}
            </div>
          </>
        )}

        {tab === 'mine' && (
          <>
            {mine.isSuccess && mine.data.assets.length === 0 && <p className="mt-6 text-zinc-500">{t('community.mineEmpty')}</p>}
            <div className="columns-2 gap-3 sm:columns-3 lg:columns-4 [&>*]:mb-3">
              {mine.data?.assets.map((a) => (
                <MineCard key={a.biz_id} asset={a} onUnpublish={() => unpublish.mutate(a.biz_id)} unpublishing={unpublish.isPending} />
              ))}
            </div>
          </>
        )}
      </div>

      {active && (
        <div
          onClick={() => setActive(null)}
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-6"
        >
          {/* max-h-full alone constrained this div but never gave it its own
              scroll region — a long prompt (video.sequence concatenates
              every shot's own text into one string) had nothing capping it,
              so the modal just grew past the viewport instead of scrolling
              ("太大了", found live off exactly that kind of multi-shot
              prompt). overflow-y-auto here plus a capped, independently
              scrollable prompt block below keep the image the fixed,
              dominant element regardless of how long the prompt is. */}
          <div onClick={(e) => e.stopPropagation()} className="max-h-full max-w-3xl space-y-3 overflow-y-auto">
            {active.type === 'video' ? (
              <video src={active.public_url} controls autoPlay className="max-h-[75vh] w-full rounded-xl bg-black object-contain" />
            ) : (
              <img src={active.public_url} alt="" className="max-h-[75vh] w-full rounded-xl object-contain" />
            )}
            {/* This modal's bg-black/80 overlay is a deliberately fixed,
                non-theme-reactive background (an image/video viewer stays
                dark regardless of site theme) — CLAUDE.md's own documented
                pitfall: pairing that with theme-reactive zinc-* text means
                light theme's zinc-scale override (meant for light-background
                elements) flips this text dark too, on a background that
                never got lighter — illegible, found live ("浅色版根本看不清
                楚字"). neutral-* stays fixed right along with the background. */}
            {active.prompt && (
              <p className="max-h-32 overflow-y-auto whitespace-pre-wrap text-sm text-neutral-300">{active.prompt}</p>
            )}
            <div className="flex items-center gap-3">
              {!isGuest && (
                <button
                  onClick={() => toggleLike.mutate(active)}
                  disabled={toggleLike.isPending}
                  className={`flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-sm transition disabled:opacity-50 ${
                    active.liked
                      ? 'border-red-700 text-red-400 hover:border-red-500'
                      : 'border-neutral-700 text-neutral-300 hover:border-neutral-500'
                  }`}
                >
                  <HeartIcon filled={active.liked} className="h-4 w-4" />
                  {active.like_count}
                </button>
              )}
              <button
                onClick={() => setActive(null)}
                className="rounded-lg border border-neutral-700 px-4 py-1.5 text-sm text-neutral-300 transition hover:border-neutral-500"
              >
                {t('common.close')}
              </button>
            </div>
          </div>
        </div>
      )}
    </AppShell>
  )
}

// StreakPanel surfaces handleCommunityStreak's read model — GetStatus's own
// doc covers the milestone/cap rules this just renders. A milestone with
// monthly_cap: 0 (currently only the 30-day one) never shows a used/cap
// line since there's no cap to report. (A full calendar view was tried here
// once and reverted — grid-cols-7 + aspect-square cells stretched to fill
// this page's max-w-5xl width, making every day cell enormous; the product
// call was to drop it rather than fix the sizing at the time. PublishHeatmap
// below is the same per-day idea brought back with that fixed: real pixel
// sizes instead of a stretched grid, small enough to sit inline here as one
// more chip alongside the milestones instead of its own page section.)
function StreakPanel({ streak }: { streak: CommunityStreak }) {
  const { t } = useTranslation()
  return (
    <div className="mb-6 flex flex-wrap items-center gap-4 rounded-xl border border-zinc-800 bg-zinc-900/60 px-4 py-3">
      <div>
        <p className="text-xs text-zinc-500">{t('community.streak.heading')}</p>
        <p className="text-sm font-medium text-zinc-200">
          {streak.current_streak > 0
            ? t('community.streak.current', { count: streak.current_streak })
            : t('community.streak.current_zero')}
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        {streak.milestones.map((m) => (
          <div
            key={m.days}
            className={`rounded-lg border px-2.5 py-1 text-xs ${
              streak.current_streak >= m.days ? 'border-violet-700 text-violet-300' : 'border-zinc-700 text-zinc-500'
            }`}
          >
            <div>{t('community.streak.milestone', { days: m.days, credits: m.credits })}</div>
            {m.monthly_cap > 0 && (
              <div className="text-zinc-600">{t('community.streak.capUsed', { used: m.used_this_month, cap: m.monthly_cap })}</div>
            )}
          </div>
        ))}
      </div>
      <PublishHeatmap publishedDates={streak.published_dates} />
    </div>
  )
}

// PublishHeatmap: a GitHub-contributions-style grid, deliberately tiny
// (6px cells, 1px gap — the whole thing is under 90px wide) so it reads as
// one more compact chip next to the milestones rather than a page section.
// 12 columns of weeks x 7 rows of weekdays covers published_dates' own
// 84-day window (communitysvc.historyDays) exactly.
function PublishHeatmap({ publishedDates }: { publishedDates: string[] }) {
  const { t } = useTranslation()
  const published = new Set(publishedDates)

  // communitysvc's own doc: streaks are computed on UTC calendar days
  // (matches the DSN's loc=UTC), not the browser's local timezone — building
  // this in local time and only converting to UTC at the very end (the
  // first version of this did exactly that via `new Date().toISOString()`)
  // silently shifts "today" by a day for any positive UTC offset, so
  // "already published today" would never light up its own cell. Every date
  // here is instead computed as a UTC calendar day from the start.
  const days: { date: string; inSet: boolean }[] = []
  const now = new Date()
  const todayUTC = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate())
  const oneDayMs = 24 * 60 * 60 * 1000
  for (let i = 83; i >= 0; i--) {
    const iso = new Date(todayUTC - i * oneDayMs).toISOString().slice(0, 10)
    days.push({ date: iso, inSet: published.has(iso) })
  }
  // Pad the front so the grid always ends on today and starts on a Sunday —
  // CSS grid-flow:column fills column-by-column, so a partial leading week
  // needs explicit empty cells rather than the calendar just starting mid-column.
  const leadingPad = days.length > 0 ? new Date(days[0].date + 'T00:00:00Z').getUTCDay() : 0
  const cells: ({ date: string; inSet: boolean } | null)[] = [...Array(leadingPad).fill(null), ...days]

  return (
    <div className="ml-auto" title={t('community.streak.heatmap')}>
      <div
        className="grid grid-flow-col gap-[1.5px]"
        style={{ gridTemplateRows: 'repeat(7, 6px)', gridAutoColumns: '6px' }}
      >
        {cells.map((cell, i) =>
          cell ? (
            <div
              key={cell.date}
              title={t(cell.inSet ? 'community.streak.heatmapPublished' : 'community.streak.heatmapEmpty', { date: cell.date })}
              className={`h-[6px] w-[6px] rounded-[1px] ${cell.inSet ? 'bg-violet-500' : 'bg-zinc-800'}`}
            />
          ) : (
            <div key={`pad-${i}`} className="h-[6px] w-[6px]" />
          ),
        )}
      </div>
    </div>
  )
}

function MineCard({ asset, onUnpublish, unpublishing }: { asset: AssetResponse; onUnpublish: () => void; unpublishing: boolean }) {
  const { t } = useTranslation()
  return (
    <div className="group relative block w-full overflow-hidden rounded-xl border border-zinc-800 bg-zinc-900">
      <Link to={`/assets/${asset.biz_id}`}>
        {asset.type === 'video' ? (
          <video src={asset.public_url} muted className="block max-h-96 w-full bg-black object-contain" />
        ) : (
          <img src={asset.public_url} alt="" loading="lazy" className="block max-h-96 w-full object-cover" />
        )}
      </Link>
      {asset.resolution_tag && (
        <span className="absolute bottom-1.5 left-1.5 rounded bg-violet-500/80 px-1.5 py-0.5 font-mono text-[10px] font-medium text-white">
          {asset.resolution_tag}
        </span>
      )}
      {/* Read-only here — liking your own work isn't a real action, this is
          just letting an owner see how their own published piece is doing. */}
      {!!asset.like_count && (
        <span className="absolute bottom-1.5 right-1.5 flex items-center gap-1 rounded bg-black/60 px-1.5 py-0.5 text-[10px] font-medium text-neutral-200">
          <HeartIcon filled className="h-3 w-3" />
          {asset.like_count}
        </span>
      )}
      <button
        onClick={onUnpublish}
        disabled={unpublishing}
        className="absolute right-1.5 top-1.5 rounded-lg bg-black/60 px-2 py-1 text-xs text-white opacity-0 backdrop-blur transition hover:bg-black/80 group-hover:opacity-100 disabled:opacity-100"
      >
        {t('community.unpublish')}
      </button>
      {/* This tab had no way to see the prompt at all before — listAssets'
          lean projection never carried meta/prompt (assetToJSON's own doc),
          found live off "community 不展示提示词". */}
      {asset.prompt && <p className="line-clamp-2 px-2 py-1.5 text-xs text-zinc-400">{asset.prompt}</p>}
    </div>
  )
}
