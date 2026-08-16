import { useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, type AssetResponse } from '../lib/api'
import { useClickOutside } from '../hooks/useClickOutside'

// §04's "@ 引用素材語法" gap — jimeng's composer lets typing "@" mid-prompt
// open a picker and insert a token like "@圖片1" that also feeds that asset
// into the actual generation request, not just decorative text. A full
// mention parser (syntax highlighting, arbitrary-position edits) needs a
// rich-text editor this codebase doesn't have; this is the plain-<textarea>
// version — it only ever triggers on an "@" typed at the very end of the
// current selection, which covers the same "type @, pick something, keep
// typing" flow the PRD's screenshot shows without needing to parse mentions
// out of arbitrary cursor positions later.
export function MentionTextarea({
  value,
  onChange,
  onMentionAsset,
  rows = 3,
  placeholder,
  className = '',
}: {
  value: string
  onChange: (v: string) => void
  onMentionAsset?: (asset: AssetResponse) => void
  rows?: number
  placeholder?: string
  className?: string
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLTextAreaElement>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  // Covers the general "clicked elsewhere on the page" case; the
  // asset-picker buttons' own onMouseDown preventDefault (below) is a
  // separate, still-necessary mechanism — it stops the textarea from
  // blurring at all on a picker click, which is what let the previous
  // onBlur+setTimeout(150ms) version distinguish "picking an asset" from
  // "clicking away" in the first place. Both live inside rootRef's
  // subtree, so this hook never fires for either.
  useClickOutside(rootRef, () => setOpen(false), open)
  const recent = useQuery({
    queryKey: ['assets', 'mention-recent'],
    queryFn: () => api.listAssets({ limit: 8 }),
    enabled: open,
  })

  function handleChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    const v = e.target.value
    onChange(v)
    const pos = e.target.selectionStart
    setOpen(v.slice(0, pos).endsWith('@'))
  }

  function pick(asset: AssetResponse, index: number) {
    const el = ref.current
    const label = asset.type === 'video' ? t('studio.mention.video', { n: index }) : t('studio.mention.image', { n: index })
    if (el) {
      const pos = el.selectionStart
      const before = value.slice(0, pos)
      const after = value.slice(pos)
      if (before.endsWith('@')) {
        const next = before + label + ' ' + after
        onChange(next)
        requestAnimationFrame(() => {
          const caret = before.length + label.length + 1
          el.setSelectionRange(caret, caret)
          el.focus()
        })
      }
    }
    onMentionAsset?.(asset)
    setOpen(false)
  }

  return (
    <div ref={rootRef} className="relative">
      <textarea
        ref={ref}
        value={value}
        onChange={handleChange}
        rows={rows}
        placeholder={placeholder}
        className={`w-full resize-none rounded-xl border border-zinc-800 bg-zinc-950 p-4 outline-none focus:border-violet-500 ${className}`}
      />
      {open && (
        <div className="absolute left-0 top-full z-10 mt-1 w-72 rounded-xl border border-zinc-800 bg-zinc-900 p-2 shadow-xl">
          <p className="mb-1.5 px-1 text-[11px] uppercase tracking-wide text-zinc-500">{t('studio.mention.pickHint')}</p>
          {recent.isLoading && <p className="px-1 py-2 text-xs text-zinc-500">{t('common.loading')}</p>}
          {recent.data?.assets.length === 0 && <p className="px-1 py-2 text-xs text-zinc-500">{t('studio.mention.empty')}</p>}
          <div className="grid grid-cols-4 gap-1.5">
            {recent.data?.assets.map((a, i) => (
              <button
                key={a.biz_id}
                type="button"
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => pick(a, i + 1)}
                className="group overflow-hidden rounded-lg border border-zinc-800 hover:border-violet-500"
                title={a.type === 'video' ? t('studio.mention.video', { n: i + 1 }) : t('studio.mention.image', { n: i + 1 })}
              >
                {a.type === 'video' ? (
                  <video src={a.public_url} className="aspect-square w-full object-cover" muted />
                ) : (
                  <img src={a.public_url} alt="" className="aspect-square w-full object-cover" />
                )}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
