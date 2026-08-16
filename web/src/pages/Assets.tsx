import { useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, type TrashAsset } from '../lib/api'
import AppShell from '../components/AppShell'
import { useToast } from '../components/Toast'
import { useDebouncedValue } from '../hooks/useDebouncedValue'

const PAGE_SIZE = 60

// F2.4: browse every asset the user has ever generated, filterable by type
// and by project (Projects.tsx's own "查看资产" link lands here with
// ?project_id= preset — the URL param is the shared state, not local
// component state, so that link and this page's own dropdown stay in sync).
// F2.5's "以此再生成"/generation-params view lives one level deeper, at
// /assets/:id (AssetDetail.tsx) — this grid's job is just getting the user
// there, plus the bulk-operation surface (select/delete/batch-download)
// that only makes sense at the grid level.
type Filter = 'all' | 'image' | 'video'

export default function Assets() {
  const { t } = useTranslation()
  const [filter, setFilter] = useState<Filter>('all')
  const [selected, setSelected] = useState<string[]>([])
  const [searchParams, setSearchParams] = useSearchParams()
  const projectId = searchParams.get('project_id') ?? ''
  const [search, setSearch] = useState('')
  const debouncedSearch = useDebouncedValue(search, 300)
  // §07's "資產庫瀑布流／虛擬滾動" gap — this codebase has no windowed-list
  // library and GET /assets has no cursor pagination (unlike GET /jobs), so
  // real DOM virtualization is out of scope for this pass. What's real here:
  // the grid only ever renders `limit` rows client-side, growing by
  // PAGE_SIZE as an IntersectionObserver sentinel comes into view, instead
  // of the previous flat "fetch 120, that's the whole library" cap — a
  // library past 120 assets was simply truncated with no way to see the
  // rest before this.
  const [limit, setLimit] = useState(PAGE_SIZE)
  const sentinelRef = useRef<HTMLDivElement>(null)
  const queryClient = useQueryClient()
  const pushToast = useToast()
  const navigate = useNavigate()
  // 回收站: a toggle rather than a separate route — it reuses this same
  // masonry layout and header chrome, just swapping the data source and the
  // per-card actions (restore + days-left instead of delete/select/assign).
  const [showTrash, setShowTrash] = useState(false)

  useEffect(() => setLimit(PAGE_SIZE), [filter, projectId, debouncedSearch])

  useEffect(() => {
    const el = sentinelRef.current
    if (!el) return
    const obs = new IntersectionObserver((entries) => {
      if (entries[0]?.isIntersecting) setLimit((cur) => cur + PAGE_SIZE)
    })
    obs.observe(el)
    return () => obs.disconnect()
  }, [])

  const projects = useQuery({ queryKey: ['projects'], queryFn: api.listProjects })
  const assets = useQuery({
    queryKey: ['assets', filter === 'all' ? undefined : filter, projectId || undefined, debouncedSearch || undefined, limit, 'library'],
    queryFn: () =>
      api.listAssets({
        ...(filter === 'all' ? {} : { type: filter }),
        ...(projectId ? { projectId } : {}),
        ...(debouncedSearch ? { q: debouncedSearch } : {}),
        limit,
      }),
  })
  const setProjectFilter = (id: string) => setSearchParams(id ? { project_id: id } : {}, { replace: true })

  const trash = useQuery({ queryKey: ['assets-trash'], queryFn: api.listTrash, enabled: showTrash })

  const deleteAsset = useMutation({
    mutationFn: (bizId: string) => api.deleteAsset(bizId),
    onSuccess: (_data, bizId) => {
      setSelected((cur) => cur.filter((id) => id !== bizId))
      queryClient.invalidateQueries({ queryKey: ['assets'] })
      queryClient.invalidateQueries({ queryKey: ['assets-trash'] })
    },
    onError: () => pushToast(t('assets.deleteFailed')),
  })

  const restoreAsset = useMutation({
    mutationFn: (bizId: string) => api.restoreAsset(bizId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['assets-trash'] })
      queryClient.invalidateQueries({ queryKey: ['assets'] })
    },
    onError: () => pushToast(t('assets.restoreFailed')),
  })

  // F2.7's batch download: the response is a zip blob, not JSON — trigger a
  // regular browser download via a throwaway <a> + object URL, same pattern
  // as any client-side blob download.
  const batchDownload = useMutation({
    mutationFn: () => api.batchDownloadAssets(selected),
    onSuccess: (blob) => {
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'assets.zip'
      a.click()
      URL.revokeObjectURL(url)
    },
    onError: () => pushToast(t('assets.batchDownloadFailed')),
  })

  const toggleSelected = (bizId: string) =>
    setSelected((cur) => (cur.includes(bizId) ? cur.filter((id) => id !== bizId) : [...cur, bizId]))

  const setAssetProject = useMutation({
    mutationFn: ({ bizId, projectId }: { bizId: string; projectId?: string }) =>
      api.setAssetProject(bizId, projectId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['assets'] }),
    onError: () => pushToast(t('assets.assignFailed')),
  })

  return (
    <AppShell>
      <div className="mx-auto max-w-5xl px-6 py-8">
        <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
          <h1 className="text-lg font-medium">{t('rail.assets')}</h1>
          <div className="flex flex-wrap items-center gap-3">
            {!showTrash && (
              <input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t('assets.searchPlaceholder')}
                className="w-40 rounded-lg border border-zinc-800 bg-zinc-950 px-2.5 py-1.5 text-sm text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-violet-500"
              />
            )}
            {!showTrash && selected.length > 0 && (
              <>
                <span className="text-sm text-zinc-500">{t('assets.selectedCount', { count: selected.length })}</span>
                <button
                  onClick={() => batchDownload.mutate()}
                  disabled={batchDownload.isPending}
                  className="rounded-lg bg-violet-500 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
                >
                  {batchDownload.isPending ? t('assets.zipping') : t('assets.batchDownload')}
                </button>
                <button
                  onClick={() => setSelected([])}
                  className="rounded-lg px-3 py-1.5 text-sm text-zinc-400 hover:bg-zinc-900"
                >
                  {t('assets.deselect')}
                </button>
              </>
            )}
            {!showTrash && (
              <select
                value={projectId}
                onChange={(e) => setProjectFilter(e.target.value)}
                className="rounded-lg border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-sm text-zinc-300 outline-none focus:border-violet-500"
              >
                <option value="">{t('assets.allProjects')}</option>
                {projects.data?.projects.map((p) => (
                  <option key={p.biz_id} value={p.biz_id}>
                    {p.name}
                  </option>
                ))}
              </select>
            )}
            {!showTrash && (
              <div className="flex gap-1">
                {(['all', 'image', 'video'] as Filter[]).map((f) => (
                  <button
                    key={f}
                    onClick={() => setFilter(f)}
                    className={`rounded-lg px-3 py-1.5 text-sm transition ${
                      filter === f
                        ? 'bg-violet-500/20 text-violet-300'
                        : 'text-zinc-400 hover:bg-zinc-900'
                    }`}
                  >
                    {t(`assets.filter.${f}`)}
                  </button>
                ))}
              </div>
            )}
            <button
              onClick={() => setShowTrash((cur) => !cur)}
              className={`rounded-lg px-3 py-1.5 text-sm transition ${
                showTrash ? 'bg-violet-500/20 text-violet-300' : 'text-zinc-400 hover:bg-zinc-900'
              }`}
            >
              {showTrash ? t('assets.library') : t('assets.trash')}
            </button>
          </div>
        </div>

        {showTrash ? (
          <>
            {trash.isSuccess && trash.data.assets.length === 0 && (
              <p className="text-zinc-500">{t('assets.trashEmpty')}</p>
            )}
            <div className="columns-2 gap-3 sm:columns-3 lg:columns-4 [&>*]:mb-3">
              {trash.data?.assets.map((a: TrashAsset) => (
                <div
                  key={a.biz_id}
                  title={a.biz_id}
                  className="group relative inline-block w-full break-inside-avoid overflow-hidden rounded-xl border border-zinc-800 bg-zinc-900"
                >
                  <button
                    onClick={() => restoreAsset.mutate(a.biz_id)}
                    disabled={restoreAsset.isPending}
                    className="absolute right-2 top-2 z-10 rounded-full bg-violet-500 px-2.5 py-1 text-xs font-medium text-white opacity-0 transition hover:bg-violet-400 disabled:opacity-50 group-hover:opacity-100"
                  >
                    {t('assets.restore')}
                  </button>
                  {a.type === 'video' ? (
                    <video src={a.public_url} controls className="block max-h-96 w-full bg-black object-contain" />
                  ) : (
                    <img src={a.public_url} alt="" loading="lazy" className="block max-h-96 w-full object-cover opacity-70" />
                  )}
                  <div className="pointer-events-none absolute bottom-1.5 left-1.5 flex gap-1">
                    {a.resolution_tag && (
                      <span className="rounded bg-violet-500/80 px-1.5 py-0.5 font-mono text-[10px] font-medium text-white">
                        {a.resolution_tag}
                      </span>
                    )}
                    <span className="rounded bg-black/60 px-1.5 py-0.5 font-mono text-[10px] text-zinc-300">
                      {t('assets.daysLeft', { count: a.days_until_purge })}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </>
        ) : (
          <>
        {assets.isSuccess && assets.data.assets.length === 0 && (
          <p className="text-zinc-500">{t('assets.empty')}</p>
        )}

        <div className="columns-2 gap-3 sm:columns-3 lg:columns-4 [&>*]:mb-3">
          {assets.data?.assets.map((a) => (
            <div
              key={a.biz_id}
              title={a.biz_id}
              onClick={() => navigate(`/assets/${a.biz_id}`)}
              className={`group relative inline-block w-full cursor-pointer break-inside-avoid overflow-hidden rounded-xl border bg-zinc-900 transition ${
                selected.includes(a.biz_id) ? 'border-violet-500' : 'border-zinc-800 hover:border-zinc-700'
              }`}
            >
              <input
                type="checkbox"
                checked={selected.includes(a.biz_id)}
                onChange={(e) => {
                  e.stopPropagation()
                  toggleSelected(a.biz_id)
                }}
                onClick={(e) => e.stopPropagation()}
                className={`absolute left-2 top-2 z-10 h-4 w-4 rounded accent-violet-500 transition-opacity ${
                  selected.includes(a.biz_id) ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'
                }`}
              />
              <button
                onClick={(e) => {
                  e.stopPropagation()
                  deleteAsset.mutate(a.biz_id)
                }}
                title={t('common.delete')}
                className="absolute right-2 top-2 z-10 flex h-6 w-6 items-center justify-center rounded-full bg-black/60 text-xs text-zinc-300 opacity-0 transition hover:text-red-400 group-hover:opacity-100"
              >
                ✕
              </button>
              {a.type === 'video' ? (
                <video
                  src={a.public_url}
                  controls
                  onClick={(e) => e.stopPropagation()}
                  className="block max-h-96 w-full bg-black object-contain"
                />
              ) : (
                <img src={a.public_url} alt="" loading="lazy" className="block max-h-96 w-full object-cover" />
              )}
              <div className="pointer-events-none absolute bottom-1.5 left-1.5 flex gap-1">
                {a.resolution_tag && (
                  <span className="rounded bg-violet-500/80 px-1.5 py-0.5 font-mono text-[10px] font-medium text-white">
                    {a.resolution_tag}
                  </span>
                )}
                {a.width > 0 && a.height > 0 && (
                  <span className="rounded bg-black/60 px-1.5 py-0.5 font-mono text-[10px] text-zinc-300">
                    {a.width}×{a.height}
                  </span>
                )}
              </div>
              <select
                value={a.project_id}
                onClick={(e) => e.stopPropagation()}
                onChange={(e) => {
                  setAssetProject.mutate({ bizId: a.biz_id, projectId: e.target.value || undefined })
                }}
                title={t('assets.assignToProject')}
                className="absolute bottom-1.5 right-1.5 z-10 max-w-[92px] truncate rounded bg-black/60 px-1 py-0.5 text-[10px] text-zinc-300 opacity-0 outline-none transition group-hover:opacity-100"
              >
                <option value="">{t('assets.unassigned')}</option>
                {projects.data?.projects.map((p) => (
                  <option key={p.biz_id} value={p.biz_id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </div>
          ))}
        </div>
        {assets.data && assets.data.assets.length >= limit && (
          <div ref={sentinelRef} className="py-6 text-center text-xs text-zinc-600">
            {assets.isFetching ? t('common.loading') : ''}
          </div>
        )}
          </>
        )}
      </div>
    </AppShell>
  )
}
