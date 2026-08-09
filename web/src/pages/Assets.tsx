import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import Nav from '../components/Nav'

// F2.4/F2.5: browse every asset the user has ever generated, filterable by
// type. "以此再生成" (regenerate from this) is out of scope here — Studio's
// AssetPicker already covers the one concrete case that matters right now
// (picking existing assets as video.single/character material); a dedicated
// "send this asset into a new job" action is a bigger cross-page flow than
// this pass covers.
type Filter = 'all' | 'image' | 'video'

export default function Assets() {
  const [filter, setFilter] = useState<Filter>('all')
  const assets = useQuery({
    queryKey: ['assets', filter === 'all' ? undefined : filter, 'library'],
    queryFn: () => api.listAssets(filter === 'all' ? { limit: 120 } : { type: filter, limit: 120 }),
  })

  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-50">
      <Nav />
      <div className="mx-auto max-w-5xl px-6 py-8">
        <div className="mb-6 flex items-center justify-between">
          <h1 className="text-lg font-medium">素材库</h1>
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

        {assets.isSuccess && assets.data.assets.length === 0 && (
          <p className="text-zinc-500">还没有生成过任何素材，去「创作台」生成一些</p>
        )}

        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
          {assets.data?.assets.map((a) => (
            <div key={a.biz_id} className="overflow-hidden rounded-xl border border-zinc-800 bg-zinc-900">
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
