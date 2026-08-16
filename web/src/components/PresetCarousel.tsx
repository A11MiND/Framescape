import { useTranslation } from 'react-i18next'
import type { Preset } from '../lib/api'

// §19.4.1's right-rail "预设卡片横滑区" — the previous build only ever
// rendered Preset.name as a text pill, leaving Preset.cover_url and
// Preset.style_type (F4.4's 漫画/元气/中世纪/水彩 mapping) completely
// unused even though the API already returns both. One horizontally
// scrolling row per category, grouped the same way Presets.tsx already
// groups them.
export default function PresetCarousel({
  presets,
  selected,
  onToggle,
  onDelete,
}: {
  presets: Preset[]
  selected: string[]
  onToggle: (id: string) => void
  // onDelete is only ever offered for presets.mine (F4.5's own saves) — a
  // seeded system preset has no delete affordance here at all, matching
  // handleDeletePreset's own ownership check on the backend.
  onDelete?: (id: string) => void
}) {
  const { t } = useTranslation()
  const grouped = presets.reduce<Record<string, Preset[]>>((acc, p) => {
    ;(acc[p.category] ??= []).push(p)
    return acc
  }, {})

  if (presets.length === 0) return null

  return (
    <div className="space-y-3">
      {Object.entries(grouped).map(([category, items]) => (
        <div key={category}>
          <p className="mb-1.5 text-xs uppercase tracking-wide text-zinc-500">
            {t(`presets.category.${category}`, category)}
          </p>
          <div className="flex gap-2 overflow-x-auto pb-1">
            {items.map((p) => {
              const active = selected.includes(p.biz_id)
              return (
                <button
                  key={p.biz_id}
                  type="button"
                  onClick={() => onToggle(p.biz_id)}
                  title={p.prompt_fragment}
                  className={`group relative h-20 w-20 shrink-0 overflow-hidden rounded-xl border-2 transition ${
                    active ? 'border-violet-500' : 'border-zinc-800 hover:border-zinc-700'
                  }`}
                >
                  {p.cover_url ? (
                    <img src={p.cover_url} alt="" className="h-full w-full object-cover" />
                  ) : (
                    <div className="flex h-full w-full items-center justify-center bg-zinc-900 text-xs text-zinc-600">
                      {t('presetCarousel.noCover')}
                    </div>
                  )}
                  <div
                    className={`absolute inset-x-0 bottom-0 truncate bg-gradient-to-t from-black/85 to-transparent px-1.5 pb-1 pt-3 text-left text-[11px] ${
                      active ? 'text-violet-300' : 'text-zinc-200'
                    }`}
                  >
                    {p.name}
                  </div>
                  {active && (
                    <span className="absolute right-1 top-1 flex h-4 w-4 items-center justify-center rounded-full bg-violet-500 text-[10px] text-white">
                      ✓
                    </span>
                  )}
                  {p.mine && onDelete && (
                    <span
                      role="button"
                      onClick={(e) => {
                        e.stopPropagation()
                        onDelete(p.biz_id)
                      }}
                      title={t('common.delete')}
                      className="absolute left-1 top-1 flex h-4 w-4 items-center justify-center rounded-full bg-black/70 text-[10px] text-zinc-300 opacity-0 transition hover:text-red-400 group-hover:opacity-100"
                    >
                      ✕
                    </span>
                  )}
                </button>
              )
            })}
          </div>
        </div>
      ))}
    </div>
  )
}
