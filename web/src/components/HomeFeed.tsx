import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, type Preset } from '../lib/api'
import { useDebouncedValue } from '../hooks/useDebouncedValue'

type FeedTab = 'recent' | 'inspiration' | 'styles'

// §07/09's "我的最近產物流 + 靈感範例 + 預設風格三個 tab" + "素材搜尋框"
// gaps — the empty-state area under the Composer only ever showed a handful
// of preset cards (Studio.tsx's old EmptyState). This is the real three-tab
// feed the blueprint's mockup called for, plus the search box that was
// missing entirely. "最近作品" reuses GET /assets (same data the /assets
// page itself shows, just the newest slice); "靈感範例" is the old
// click-to-fill preset-card behavior, unchanged; "預設風格" mirrors
// Presets.tsx's own category grouping, embedded here so browsing styles
// doesn't require leaving the composer.
export default function HomeFeed({
  presets,
  onPickFragment,
  onTogglePreset,
  selectedPresetIds,
}: {
  presets: Preset[]
  onPickFragment: (fragment: string) => void
  onTogglePreset: (id: string) => void
  selectedPresetIds: string[]
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [tab, setTab] = useState<FeedTab>('recent')
  const [search, setSearch] = useState('')
  const debouncedSearch = useDebouncedValue(search, 300)

  const recent = useQuery({
    queryKey: ['assets', 'home-feed', debouncedSearch || undefined],
    queryFn: () => api.listAssets({ limit: 12, q: debouncedSearch || undefined }),
    enabled: tab === 'recent',
  })

  const grouped = presets.reduce<Record<string, Preset[]>>((acc, p) => {
    ;(acc[p.category] ??= []).push(p)
    return acc
  }, {})

  const TABS: { value: FeedTab; labelKey: string }[] = [
    { value: 'recent', labelKey: 'homeFeed.recent' },
    { value: 'inspiration', labelKey: 'homeFeed.inspiration' },
    { value: 'styles', labelKey: 'homeFeed.styles' },
  ]

  return (
    <div className="mx-auto max-w-2xl">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <div className="flex gap-1">
          {TABS.map((tb) => (
            <button
              key={tb.value}
              onClick={() => setTab(tb.value)}
              className={`rounded-lg px-3 py-1.5 text-sm transition ${
                tab === tb.value ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-400 hover:bg-zinc-900'
              }`}
            >
              {t(tb.labelKey)}
            </button>
          ))}
        </div>
        {tab === 'recent' && (
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('homeFeed.searchPlaceholder')}
            className="w-40 rounded-lg border border-zinc-800 bg-zinc-950 px-2.5 py-1.5 text-sm text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-violet-500"
          />
        )}
      </div>

      {tab === 'recent' && (
        <>
          {recent.isSuccess && recent.data.assets.length === 0 && (
            <p className="text-center text-sm text-zinc-500">{t('homeFeed.noRecent')}</p>
          )}
          <div className="grid grid-cols-3 gap-2 sm:grid-cols-4">
            {recent.data?.assets.map((a) => (
              <button
                key={a.biz_id}
                onClick={() => navigate(`/assets/${a.biz_id}`)}
                className="group overflow-hidden rounded-xl border border-zinc-800 transition hover:border-violet-500"
              >
                {a.type === 'video' ? (
                  <video src={a.public_url} className="aspect-square w-full object-cover" muted />
                ) : (
                  <img src={a.public_url} alt="" className="aspect-square w-full object-cover" />
                )}
              </button>
            ))}
          </div>
        </>
      )}

      {tab === 'inspiration' && (
        <>
          {presets.length === 0 ? (
            <p className="text-center text-sm text-zinc-500">{t('studio.emptyResult')}</p>
          ) : (
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
              {presets.slice(0, 8).map((p) => (
                <button
                  key={p.biz_id}
                  onClick={() => onPickFragment(p.prompt_fragment)}
                  className="group overflow-hidden rounded-xl border border-zinc-800 transition hover:border-violet-500"
                >
                  {p.cover_url ? (
                    <img src={p.cover_url} alt="" className="aspect-square w-full object-cover" />
                  ) : (
                    <div className="aspect-square w-full bg-zinc-800" />
                  )}
                  <p className="truncate bg-zinc-950 px-2 py-1 text-xs text-zinc-400 group-hover:text-violet-300">{p.name}</p>
                </button>
              ))}
            </div>
          )}
        </>
      )}

      {tab === 'styles' && (
        <div className="space-y-4">
          {Object.entries(grouped).map(([category, items]) => (
            <div key={category}>
              <p className="mb-1.5 text-xs uppercase tracking-wide text-zinc-500">{t(`presets.category.${category}`, category)}</p>
              <div className="flex flex-wrap gap-1.5">
                {items.map((p) => (
                  <button
                    key={p.biz_id}
                    onClick={() => onTogglePreset(p.biz_id)}
                    title={p.prompt_fragment}
                    className={`rounded-full border px-2.5 py-1 text-xs transition ${
                      selectedPresetIds.includes(p.biz_id)
                        ? 'border-violet-500 bg-violet-500/10 text-violet-300'
                        : 'border-zinc-800 text-zinc-400 hover:border-zinc-700'
                    }`}
                  >
                    {p.name}
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
