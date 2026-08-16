import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import AppShell from '../components/AppShell'
import { useToast } from '../components/Toast'

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
  const queryClient = useQueryClient()
  const pushToast = useToast()
  const navigate = useNavigate()

  const projects = useQuery({ queryKey: ['projects'], queryFn: api.listProjects })
  const assets = useQuery({
    queryKey: ['assets', filter === 'all' ? undefined : filter, projectId || undefined, 'library'],
    queryFn: () =>
      api.listAssets({
        ...(filter === 'all' ? {} : { type: filter }),
        ...(projectId ? { projectId } : {}),
        limit: 120,
      }),
  })
  const setProjectFilter = (id: string) => setSearchParams(id ? { project_id: id } : {}, { replace: true })

  const deleteAsset = useMutation({
    mutationFn: (bizId: string) => api.deleteAsset(bizId),
    onSuccess: (_data, bizId) => {
      setSelected((cur) => cur.filter((id) => id !== bizId))
      queryClient.invalidateQueries({ queryKey: ['assets'] })
    },
    onError: () => pushToast(t('assets.deleteFailed')),
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
        <div className="mb-6 flex items-center justify-between">
          <h1 className="text-lg font-medium">{t('rail.assets')}</h1>
          <div className="flex items-center gap-3">
            {selected.length > 0 && (
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
          </div>
        </div>

        {assets.isSuccess && assets.data.assets.length === 0 && (
          <p className="text-zinc-500">{t('assets.empty')}</p>
        )}

        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
          {assets.data?.assets.map((a) => (
            <div
              key={a.biz_id}
              title={a.biz_id}
              onClick={() => navigate(`/assets/${a.biz_id}`)}
              className={`group relative cursor-pointer overflow-hidden rounded-xl border bg-zinc-900 transition ${
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
                  className="aspect-square w-full bg-black object-contain"
                />
              ) : (
                <img src={a.public_url} alt="" className="aspect-square w-full object-cover" />
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
      </div>
    </AppShell>
  )
}
