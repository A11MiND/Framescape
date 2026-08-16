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
  deletingId,
}: {
  presets: Preset[]
  selected: string[]
  onToggle: (id: string) => void
  // onDelete is only ever offered for presets.mine (F4.5's own saves) — a
  // seeded system preset has no delete affordance here at all, matching
  // handleDeletePreset's own ownership check on the backend.
  onDelete?: (id: string) => void
  // deletingId disables just the one delete button currently in flight —
  // a code-review pass caught that a fast double-click fired two DELETE
  // requests, with the second one 404ing (row already gone) and surfacing
  // a false "failed to delete" toast for a deletion that actually
  // succeeded. The caller (Studio.tsx) derives this from its mutation's
  // own isPending/variables.
  deletingId?: string
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
              const deleting = deletingId === p.biz_id
              return (
                // A real <button> can't contain another real <button> (invalid
                // HTML, and the reason the delete control below used to be a
                // non-focusable <span role="button"> that keyboard/screen-reader
                // users could never reach — caught in code review). This is a
                // <div role="button"> instead specifically so the delete
                // control can be a genuine nested <button>.
                <div
                  key={p.biz_id}
                  role="button"
                  tabIndex={0}
                  onClick={() => onToggle(p.biz_id)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      onToggle(p.biz_id)
                    }
                  }}
                  title={p.prompt_fragment}
                  className={`group relative h-20 w-20 shrink-0 cursor-pointer overflow-hidden rounded-xl border-2 transition focus:outline-none focus-visible:ring-2 focus-visible:ring-violet-500 ${
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
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation()
                        if (!deleting) onDelete(p.biz_id)
                      }}
                      disabled={deleting}
                      title={t('common.delete')}
                      // opacity-60 (not opacity-0) by default — a hover-only
                      // reveal leaves this undiscoverable/untappable on
                      // touch-only devices (no persistent :hover), also
                      // caught in code review. group-focus-within covers
                      // keyboard users tabbing onto the button itself.
                      className="absolute left-1 top-1 flex h-4 w-4 items-center justify-center rounded-full bg-black/70 text-[10px] text-zinc-300 opacity-60 transition hover:text-red-400 hover:opacity-100 focus:opacity-100 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-red-400 group-hover:opacity-100 group-focus-within:opacity-100 disabled:opacity-50"
                    >
                      ✕
                    </button>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      ))}
    </div>
  )
}
