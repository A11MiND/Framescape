import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api, type Character } from '../lib/api'

// §07's "角色槽 A/B 缺頭像縮圖／快捷新建" gap — the plain <select>
// CharacterSelectInline used couldn't show a thumbnail at all (native
// <option> elements are text-only), so this replaces it with a small
// button + popover that can. "快捷新建" is the Link at the bottom of the
// popover straight to /characters rather than a second inline creation
// form — Characters.tsx's CharacterForm already exists and does real
// asset-picker-backed ref image selection, duplicating that here would
// diverge from it over time.
export function CharacterSlotPicker({
  value,
  onChange,
  options,
}: {
  value: string
  onChange: (v: string) => void
  options: Character[]
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const selected = options.find((c) => c.biz_id === value)

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1.5 text-zinc-100"
      >
        {selected ? <CharacterAvatar character={selected} size={16} /> : null}
        {selected ? selected.name : t('studio.unselected')}
        <span className="text-zinc-600">▾</span>
      </button>
      {open && (
        <div className="absolute left-0 top-full z-10 mt-1 w-56 rounded-xl border border-zinc-800 bg-zinc-900 p-1.5 shadow-xl">
          <button
            type="button"
            onClick={() => {
              onChange('')
              setOpen(false)
            }}
            className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-xs text-zinc-400 hover:bg-zinc-800"
          >
            {t('studio.unselected')}
          </button>
          {options.map((c) => (
            <button
              key={c.biz_id}
              type="button"
              onClick={() => {
                onChange(c.biz_id)
                setOpen(false)
              }}
              className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-xs text-zinc-200 hover:bg-zinc-800"
            >
              <CharacterAvatar character={c} size={20} />
              <span className="truncate">{c.name}</span>
            </button>
          ))}
          <Link
            to="/characters"
            onClick={() => setOpen(false)}
            className="mt-1 flex items-center gap-2 rounded-lg border-t border-zinc-800 px-2 py-1.5 pt-2 text-xs text-violet-400 hover:text-violet-300"
          >
            + {t('characters.new')}
          </Link>
        </div>
      )}
    </div>
  )
}

function CharacterAvatar({ character, size }: { character: Character; size: number }) {
  const assetId = character.ref_asset_ids[0]
  const { data } = useQuery({
    queryKey: ['asset', assetId],
    queryFn: () => api.getAsset(assetId),
    enabled: !!assetId,
  })
  if (!data) {
    return <div className="shrink-0 rounded-full bg-zinc-800" style={{ width: size, height: size }} />
  }
  return (
    <img
      src={data.public_url}
      alt=""
      className="shrink-0 rounded-full object-cover"
      style={{ width: size, height: size }}
    />
  )
}
