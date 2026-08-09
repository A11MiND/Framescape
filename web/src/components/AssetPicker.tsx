import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, type AssetResponse } from '../lib/api'

// Shared thumbnail-grid picker over the user's already-generated assets —
// there's no raw upload endpoint (PRD §11: assets only exist as generation
// side-effects), so every "pick an image/video" UI (character refs,
// video.single first/last frame + reference material) draws from here.
//
// Collapsed by default, showing only currently-selected thumbnails plus a
// "+ 选择" affordance — video.single alone uses four of these on one page
// (first/last frame, reference images, reference videos), and rendering
// every one of a user's 30+ assets inline on all four at once was the
// single biggest source of page bloat before this redesign. Expanding
// reveals a height-capped, internally-scrolling grid instead of pushing the
// rest of the page down.
export function AssetPicker({
  type,
  selected,
  onToggle,
  max,
  disabled,
  emptyHint,
}: {
  type: 'image' | 'video'
  selected: string[]
  onToggle: (id: string) => void
  max?: number
  disabled?: boolean
  emptyHint?: string
}) {
  const [expanded, setExpanded] = useState(false)
  const assets = useQuery({
    queryKey: ['assets', type],
    queryFn: () => api.listAssets({ type, limit: 60 }),
  })

  if (assets.isSuccess && assets.data.assets.length === 0) {
    return (
      <p className="text-sm text-zinc-600">
        {emptyHint ?? `还没有生成过${type === 'image' ? '图片' : '视频'}`}
      </p>
    )
  }

  const all = assets.data?.assets ?? []
  const selectedAssets = all.filter((a) => selected.includes(a.biz_id))

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        {selectedAssets.map((a) => (
          <Thumbnail
            key={a.biz_id}
            asset={a}
            type={type}
            active
            disabled={disabled}
            onClick={() => onToggle(a.biz_id)}
          />
        ))}
        <button
          type="button"
          disabled={disabled}
          onClick={() => setExpanded((v) => !v)}
          className="flex h-14 w-14 shrink-0 items-center justify-center rounded-lg border border-dashed border-zinc-700 text-xs text-zinc-500 transition hover:border-zinc-500 hover:text-zinc-300 disabled:cursor-not-allowed disabled:opacity-40"
        >
          {expanded ? '收起' : '+ 选择'}
        </button>
      </div>

      {expanded && (
        <div className="flex max-h-52 flex-wrap gap-2 overflow-y-auto rounded-lg border border-zinc-800 bg-black/20 p-2">
          {all.map((a) => {
            const active = selected.includes(a.biz_id)
            const atMax = !!max && !active && selected.length >= max
            return (
              <Thumbnail
                key={a.biz_id}
                asset={a}
                type={type}
                active={active}
                disabled={disabled || atMax}
                onClick={() => onToggle(a.biz_id)}
              />
            )
          })}
        </div>
      )}
    </div>
  )
}

function Thumbnail({
  asset,
  type,
  active,
  disabled,
  onClick,
}: {
  asset: AssetResponse
  type: 'image' | 'video'
  active: boolean
  disabled?: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={`shrink-0 overflow-hidden rounded-lg border-2 transition disabled:cursor-not-allowed disabled:opacity-40 ${
        active ? 'border-violet-500' : 'border-transparent hover:border-zinc-700'
      }`}
    >
      {type === 'image' ? (
        <img src={asset.public_url} alt="" className="h-14 w-14 object-cover" />
      ) : (
        <video src={asset.public_url} className="h-14 w-14 object-cover" muted />
      )}
    </button>
  )
}
