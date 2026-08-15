import { useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type AssetResponse } from '../lib/api'
import { uploadAsset } from '../lib/upload'

// Shared thumbnail-grid picker over the user's assets — every "pick an
// image/video" UI (character refs, video.single first/last frame +
// reference material) draws from here. F2.1 (POST /assets/upload-url +
// .../complete) added the "+ 上传" affordance below; before that endpoint
// existed this could only ever show assets already produced by a
// generation, which meant a brand-new account's every picker was
// permanently empty until they generated something first.
//
// Collapsed by default, showing only currently-selected thumbnails plus the
// two action buttons — video.single alone uses four of these on one page
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
  const fileInputRef = useRef<HTMLInputElement>(null)
  const qc = useQueryClient()
  const assets = useQuery({
    queryKey: ['assets', type],
    queryFn: () => api.listAssets({ type, limit: 60 }),
  })

  const upload = useMutation({
    mutationFn: (file: File) => uploadAsset(file),
    onSuccess: (asset) => {
      qc.invalidateQueries({ queryKey: ['assets'] })
      if (!max || selected.length < max) onToggle(asset.biz_id)
    },
  })

  const all = assets.data?.assets ?? []
  const selectedAssets = all.filter((a) => selected.includes(a.biz_id))
  const empty = assets.isSuccess && all.length === 0

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
        {!empty && (
          <button
            type="button"
            disabled={disabled}
            onClick={() => setExpanded((v) => !v)}
            className="flex h-14 w-14 shrink-0 items-center justify-center rounded-lg border border-dashed border-zinc-700 text-xs text-zinc-500 transition hover:border-zinc-500 hover:text-zinc-300 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {expanded ? '收起' : '+ 选择'}
          </button>
        )}
        <button
          type="button"
          disabled={disabled || upload.isPending}
          onClick={() => fileInputRef.current?.click()}
          className="flex h-14 w-14 shrink-0 flex-col items-center justify-center gap-0.5 rounded-lg border border-dashed border-zinc-700 text-xs text-zinc-500 transition hover:border-violet-500 hover:text-violet-300 disabled:cursor-not-allowed disabled:opacity-40"
        >
          {upload.isPending ? (
            <span className="animate-pulse">上传中</span>
          ) : (
            <>
              <span className="text-base leading-none">⇧</span>
              上传
            </>
          )}
        </button>
        <input
          ref={fileInputRef}
          type="file"
          accept={type === 'image' ? 'image/*' : 'video/*'}
          className="hidden"
          onChange={(e) => {
            const file = e.target.files?.[0]
            e.target.value = ''
            if (file) upload.mutate(file)
          }}
        />
      </div>

      {empty && <p className="text-sm text-zinc-600">{emptyHint ?? `还没有${type === 'image' ? '图片' : '视频'}，点右侧「上传」添加`}</p>}
      {upload.isError && (
        <p className="text-xs text-red-400">{upload.error instanceof ApiError ? upload.error.message : '上传失败，请重试'}</p>
      )}

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
