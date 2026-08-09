import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'

// Shared thumbnail-grid picker over the user's already-generated assets —
// there's no raw upload endpoint (PRD §11: assets only exist as generation
// side-effects), so every "pick an image/video" UI (character refs,
// video.single first/last frame + reference material) draws from here.
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

  return (
    <div className="flex flex-wrap gap-2">
      {assets.data?.assets.map((a) => {
        const active = selected.includes(a.biz_id)
        const atMax = !!max && !active && selected.length >= max
        return (
          <button
            key={a.biz_id}
            type="button"
            disabled={disabled || atMax}
            onClick={() => onToggle(a.biz_id)}
            className={`overflow-hidden rounded-lg border-2 transition disabled:cursor-not-allowed disabled:opacity-40 ${
              active ? 'border-violet-500' : 'border-transparent hover:border-zinc-700'
            }`}
          >
            {type === 'image' ? (
              <img src={a.public_url} alt="" className="h-20 w-20 object-cover" />
            ) : (
              <video src={a.public_url} className="h-20 w-20 object-cover" muted />
            )}
          </button>
        )
      })}
    </div>
  )
}
