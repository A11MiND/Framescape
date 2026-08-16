import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, type AssetResponse } from '../lib/api'
import { useClickOutside } from '../hooks/useClickOutside'

// Plain 4-point sparkle, straight lines only (no hand-drawn curves) —
// §07's "✨ AI 改寫按鈕，不要用emoji" ask, same reasoning as every other
// icon replaced this session (NotificationCenter's bell, the format
// cards): a real glyph instead of a platform-inconsistent emoji.
function SparkleIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 20 20" fill="currentColor" className={className}>
      <path d="M10 1l2.2 6.8L19 10l-6.8 2.2L10 19l-2.2-6.8L1 10l6.8-2.2z" />
    </svg>
  )
}

// Matches an inserted mention token ("#图片1", "#视频2", ...) anywhere in the
// text, for the highlight overlay below.
const MENTION_RE = /#[^\s#]*/g

function renderHighlighted(text: string) {
  const nodes: React.ReactNode[] = []
  let last = 0
  let m: RegExpExecArray | null
  const re = new RegExp(MENTION_RE)
  while ((m = re.exec(text))) {
    if (m.index > last) nodes.push(text.slice(last, m.index))
    nodes.push(
      <span key={m.index} className="rounded bg-violet-500/25 text-violet-300">
        {m[0]}
      </span>,
    )
    last = m.index + m[0].length
  }
  nodes.push(text.slice(last) + '​') // trailing zero-width space: preserves a real trailing newline's line height
  return nodes
}

// §07's "打字機效果教用戶用#" ask: cycles through a couple of example
// phrases with a type/pause/erase animation instead of a single static
// placeholder sitting there looking like real (if slightly grey) content —
// a user specifically pushed back on the old prefilled-example pattern for
// exactly that "looks like it's already decided what to generate" reason.
// Stops entirely once the field has real content or focus, same as a plain
// placeholder would, so it never competes with what the user is typing.
function useTypewriterHint(phrases: string[], active: boolean) {
  const [text, setText] = useState('')
  useEffect(() => {
    if (!active || phrases.length === 0) {
      setText('')
      return
    }
    let phraseIndex = 0
    let charIndex = 0
    let erasing = false
    let cancelled = false
    function tick() {
      if (cancelled) return
      const phrase = phrases[phraseIndex]
      if (!erasing) {
        charIndex++
        setText(phrase.slice(0, charIndex))
        if (charIndex >= phrase.length) {
          erasing = true
          setTimeout(tick, 1600)
          return
        }
        setTimeout(tick, 45)
      } else {
        charIndex--
        setText(phrase.slice(0, charIndex))
        if (charIndex <= 0) {
          erasing = false
          phraseIndex = (phraseIndex + 1) % phrases.length
          setTimeout(tick, 300)
          return
        }
        setTimeout(tick, 20)
      }
    }
    const startTimer = setTimeout(tick, 300)
    return () => {
      cancelled = true
      clearTimeout(startTimer)
    }
  }, [active, phrases])
  return text
}

// §04's "@ 引用素材語法" gap, now "#" instead (a user asked for the swap —
// distinct enough from social platforms' "@mention" not to read as copying
// one) — jimeng's composer lets typing "#" mid-prompt open a picker and
// insert a token like "#圖片1" that also feeds that asset into the actual
// generation request, not just decorative text. A full mention parser
// (rich-text editing at arbitrary cursor positions) needs an editor this
// codebase doesn't have; this is the plain-<textarea> version — it only
// ever triggers on a "#" typed at the very end of the current selection,
// and highlights inserted tokens via a same-text overlay <div> sitting
// behind a color-transparent textarea (kept in scroll-sync), the standard
// technique for styling substrings a native textarea can't style itself.
export function MentionTextarea({
  value,
  onChange,
  onMentionAsset,
  onMentionShot,
  siblingShots,
  rows = 3,
  placeholder,
  hintPhrases,
  className = '',
  enableRewrite = true,
}: {
  value: string
  onChange: (v: string) => void
  onMentionAsset?: (asset: AssetResponse) => void
  // image.sequence's cross-shot referencing: #-referencing an earlier shot
  // in the same batch (not yet a real asset — it hasn't generated yet)
  // instead of an existing library asset. Fires with that shot's 1-based
  // index; ShotList (Studio.tsx) turns this into a per-shot dependency,
  // Spec.ShotSourceRefs' own doc covers why it's backward-only.
  onMentionShot?: (shotIndex: number) => void
  // Earlier shots in the current batch, offered as pickable mention targets
  // above the regular asset grid when non-empty. Callers pass only shots
  // that come before the one being edited — see ShotList, which is the only
  // caller that ever sets this.
  siblingShots?: { index: number; text: string }[]
  rows?: number
  // Static fallback placeholder (used as-is if hintPhrases isn't given).
  placeholder?: string
  // Animated typewriter hint phrases — cycles through these instead of a
  // static placeholder. Falls back to `placeholder` (no animation) when
  // omitted, and always includes the "#" tip as its last phrase.
  hintPhrases?: string[]
  className?: string
  // The ✨ AI-rewrite corner button — on by default since every current
  // caller is a free-text prompt field it makes sense for; a caller can
  // still opt out if a future use of this component isn't one.
  enableRewrite?: boolean
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [focused, setFocused] = useState(false)
  const ref = useRef<HTMLTextAreaElement>(null)
  const overlayRef = useRef<HTMLDivElement>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const rewrite = useMutation({
    mutationFn: () => api.rewritePrompt(value),
    onSuccess: (res) => onChange(res.text),
  })
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

  // Folds the old static `placeholder` string in as a single-phrase fallback
  // so every existing caller gets the animation for free, not just ones
  // updated to pass hintPhrases explicitly — either way there's always a
  // real animated hint once any base phrase exists, never a native
  // ::placeholder sitting underneath a color-transparent textarea (which
  // renders invisible in most browsers, since ::placeholder mostly follows
  // the element's own color).
  const phrases = useMemo(() => {
    const base = hintPhrases?.length ? hintPhrases : placeholder ? [placeholder] : []
    return base.length ? [...base, t('studio.mention.typeHint')] : []
  }, [hintPhrases, placeholder, t])
  const typedHint = useTypewriterHint(phrases, value === '' && !focused)

  function handleChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    const v = e.target.value
    onChange(v)
    const pos = e.target.selectionStart
    setOpen(v.slice(0, pos).endsWith('#'))
  }

  function pick(asset: AssetResponse, index: number) {
    const el = ref.current
    const label = asset.type === 'video' ? t('studio.mention.video', { n: index }) : t('studio.mention.image', { n: index })
    if (el) {
      const pos = el.selectionStart
      const before = value.slice(0, pos)
      const after = value.slice(pos)
      if (before.endsWith('#')) {
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

  function pickShot(shotIndex: number) {
    const el = ref.current
    const label = t('studio.mention.shot', { n: shotIndex })
    if (el) {
      const pos = el.selectionStart
      const before = value.slice(0, pos)
      const after = value.slice(pos)
      if (before.endsWith('#')) {
        const next = before + label + ' ' + after
        onChange(next)
        requestAnimationFrame(() => {
          const caret = before.length + label.length + 1
          el.setSelectionRange(caret, caret)
          el.focus()
        })
      }
    }
    onMentionShot?.(shotIndex)
    setOpen(false)
  }

  return (
    <div ref={rootRef} className="relative">
      {/* Overlay shows what should actually be visible (plain text at
          normal color, "#mentions" highlighted, or the typewriter hint) —
          the real textarea underneath stays permanently color-transparent
          (below) so it never double-renders its own plain-color text on
          top of this. Both share identical font/padding/wrapping so the
          overlay's text lines up exactly with the real caret position. */}
      <div
        ref={overlayRef}
        aria-hidden
        className="pointer-events-none absolute inset-0 overflow-hidden whitespace-pre-wrap break-words rounded-xl bg-zinc-950 p-4 text-zinc-100"
      >
        {value ? renderHighlighted(value) : <span className="text-zinc-600">{typedHint}</span>}
      </div>
      <textarea
        ref={ref}
        value={value}
        onChange={handleChange}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        onScroll={(e) => {
          if (overlayRef.current) overlayRef.current.scrollTop = e.currentTarget.scrollTop
        }}
        rows={rows}
        className={`relative w-full resize-none rounded-xl border border-zinc-800 bg-transparent p-4 text-transparent caret-zinc-100 outline-none focus:border-violet-500 ${className}`}
      />
      {enableRewrite && (
        <button
          type="button"
          onClick={() => rewrite.mutate()}
          disabled={rewrite.isPending || !value.trim()}
          title={t('studio.rewrite.button')}
          className="absolute bottom-2.5 right-2.5 flex h-7 w-7 items-center justify-center rounded-full bg-zinc-900/90 text-violet-400 transition hover:bg-zinc-800 hover:text-violet-300 disabled:opacity-40"
        >
          <SparkleIcon className={`h-4 w-4 ${rewrite.isPending ? 'animate-pulse' : ''}`} />
        </button>
      )}
      {rewrite.isError && <p className="mt-1 text-xs text-red-400">{t('studio.rewrite.failed')}</p>}
      {open && (
        <div className="absolute left-0 top-full z-10 mt-1 w-72 rounded-xl border border-zinc-800 bg-zinc-900 p-2 shadow-xl">
          {siblingShots && siblingShots.length > 0 && (
            <div className="mb-2 border-b border-zinc-800 pb-2">
              <p className="mb-1.5 px-1 text-[11px] uppercase tracking-wide text-zinc-500">{t('studio.mention.shotPickHint')}</p>
              <div className="flex flex-wrap gap-1.5">
                {siblingShots.map((shot) => (
                  <button
                    key={shot.index}
                    type="button"
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => pickShot(shot.index)}
                    title={shot.text}
                    className="rounded-lg border border-zinc-800 px-2 py-1 text-xs text-zinc-300 transition hover:border-violet-500 hover:text-violet-300"
                  >
                    {t('studio.mention.shot', { n: shot.index })}
                  </button>
                ))}
              </div>
            </div>
          )}
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
