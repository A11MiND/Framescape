import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import Nav from '../components/Nav'
import { useToast } from '../components/Toast'

// F2.4/F2.5: browse every asset the user has ever generated, filterable by
// type. "以此再生成" (regenerate from this) is out of scope here — Studio's
// AssetPicker already covers the one concrete case that matters right now
// (picking existing assets as video.single/character material); a dedicated
// "send this asset into a new job" action is a bigger cross-page flow than
// this pass covers.
type Filter = 'all' | 'image' | 'video'

export default function Assets() {
  const [filter, setFilter] = useState<Filter>('all')
  const [selected, setSelected] = useState<string[]>([])
  const queryClient = useQueryClient()
  const pushToast = useToast()

  const assets = useQuery({
    queryKey: ['assets', filter === 'all' ? undefined : filter, 'library'],
    queryFn: () => api.listAssets(filter === 'all' ? { limit: 120 } : { type: filter, limit: 120 }),
  })

  const deleteAsset = useMutation({
    mutationFn: (bizId: string) => api.deleteAsset(bizId),
    onSuccess: (_data, bizId) => {
      setSelected((cur) => cur.filter((id) => id !== bizId))
      queryClient.invalidateQueries({ queryKey: ['assets'] })
    },
    onError: () => pushToast('删除失败，请重试'),
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
    onError: () => pushToast('批量下载失败，请重试'),
  })

  const toggleSelected = (bizId: string) =>
    setSelected((cur) => (cur.includes(bizId) ? cur.filter((id) => id !== bizId) : [...cur, bizId]))

  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-50">
      <Nav />
      <div className="mx-auto max-w-5xl px-6 py-8">
        <div className="mb-6 flex items-center justify-between">
          <h1 className="text-lg font-medium">素材库</h1>
          <div className="flex items-center gap-3">
            {selected.length > 0 && (
              <>
                <span className="text-sm text-zinc-500">已选 {selected.length}</span>
                <button
                  onClick={() => batchDownload.mutate()}
                  disabled={batchDownload.isPending}
                  className="rounded-lg bg-violet-500 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
                >
                  {batchDownload.isPending ? '打包中…' : '批量下载'}
                </button>
                <button
                  onClick={() => setSelected([])}
                  className="rounded-lg px-3 py-1.5 text-sm text-zinc-400 hover:bg-zinc-900"
                >
                  取消选择
                </button>
              </>
            )}
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
                  {f === 'all' ? '全部' : f === 'image' ? '图片' : '视频'}
                </button>
              ))}
            </div>
          </div>
        </div>

        {assets.isSuccess && assets.data.assets.length === 0 && (
          <p className="text-zinc-500">还没有生成过任何素材，去「创作台」生成一些</p>
        )}

        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
          {assets.data?.assets.map((a) => (
            <div
              key={a.biz_id}
              className={`group relative overflow-hidden rounded-xl border bg-zinc-900 ${
                selected.includes(a.biz_id) ? 'border-violet-500' : 'border-zinc-800'
              }`}
            >
              <input
                type="checkbox"
                checked={selected.includes(a.biz_id)}
                onChange={() => toggleSelected(a.biz_id)}
                className="absolute left-2 top-2 z-10 accent-violet-500"
              />
              <button
                onClick={() => deleteAsset.mutate(a.biz_id)}
                title="软删除"
                className="absolute right-2 top-2 z-10 rounded-full bg-black/60 px-2 py-0.5 text-xs text-zinc-300 opacity-0 transition hover:text-red-400 group-hover:opacity-100"
              >
                ✕
              </button>
              {a.type === 'video' ? (
                <video src={a.public_url} controls className="aspect-square w-full bg-black object-contain" />
              ) : (
                <img src={a.public_url} alt="" className="aspect-square w-full object-cover" />
              )}
              <p className="truncate p-2 font-mono text-xs text-zinc-500">{a.biz_id}</p>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
