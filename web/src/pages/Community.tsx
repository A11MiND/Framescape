import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, type CommunityAsset } from '../lib/api'
import AppShell from '../components/AppShell'

// §07's "社區功能：看別人做的作品，也可以發佈出去" ask — a masonry feed of
// every published asset across every account (handleCommunityFeed's own
// doc), same visual language as Assets.tsx's library grid but deliberately
// its own page rather than a mode of that one: nothing here is the current
// user's own to delete/reassign/download-in-bulk, so none of that surface
// belongs on these cards. Clicking a card opens an inline lightbox instead
// of navigating to /assets/:id — that route is strictly "WHERE user_id =
// caller" (handleGetAsset's own doc, kept that way on purpose), so a
// community item, owned by someone else, was never going to be viewable
// there without either weakening that check or teaching the page to hide
// owner-only actions for a stranger's asset. A local lightbox sidesteps
// the question entirely instead of touching that boundary.
export default function Community() {
  const { t } = useTranslation()
  const feed = useQuery({ queryKey: ['community', 'feed'], queryFn: () => api.listCommunityFeed(90) })
  const [active, setActive] = useState<CommunityAsset | null>(null)

  return (
    <AppShell>
      <div className="mx-auto max-w-5xl px-6 py-8">
        <div className="mb-2">
          <h1 className="text-lg font-medium">{t('community.title')}</h1>
          <p className="mt-1 text-sm text-zinc-500">{t('community.subtitle')}</p>
        </div>

        {feed.isSuccess && feed.data.assets.length === 0 && <p className="mt-6 text-zinc-500">{t('community.empty')}</p>}

        <div className="mt-6 columns-2 gap-3 sm:columns-3 lg:columns-4 [&>*]:mb-3">
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
            </button>
          ))}
        </div>
      </div>

      {active && (
        <div
          onClick={() => setActive(null)}
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-6"
        >
          <div onClick={(e) => e.stopPropagation()} className="max-h-full max-w-3xl space-y-3">
            {active.type === 'video' ? (
              <video src={active.public_url} controls autoPlay className="max-h-[75vh] w-full rounded-xl bg-black object-contain" />
            ) : (
              <img src={active.public_url} alt="" className="max-h-[75vh] w-full rounded-xl object-contain" />
            )}
            {active.prompt && <p className="text-sm text-zinc-300">{active.prompt}</p>}
            <button
              onClick={() => setActive(null)}
              className="rounded-lg border border-zinc-700 px-4 py-1.5 text-sm text-zinc-300 transition hover:border-zinc-500"
            >
              {t('common.close')}
            </button>
          </div>
        </div>
      )}
    </AppShell>
  )
}
