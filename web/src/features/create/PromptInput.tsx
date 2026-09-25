import { useId, useRef, useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Sparkles } from 'lucide-react'
import { Button, Dialog, Field, Textarea, cn } from '../../ui'
import { createApi } from '../../lib/api/create'
import { errorText } from '../../lib/errorText'
import { useToast } from '../../components/Toast'
import type { AssetResponse, Character } from '../../lib/api'

interface Mention {
  kind: '@' | '#'
  start: number
  query: string
}

function findMention(text: string, caret: number): Mention | null {
  const m = text.slice(0, caret).match(/(^|\s)([@#])([^\s@#]*)$/)
  if (!m) return null
  return { kind: m[2] as '@' | '#', start: caret - m[3].length - 1, query: m[3] }
}

export interface PromptInputProps {
  value: string
  onChange: (v: string) => void
  maxChars: number
  label: string
  /** Enables @ mentions; picking one binds the character. */
  characters?: Character[]
  onMentionCharacter?: (c: Character) => void
  /** Enables # mentions of recent images; picking one attaches it. */
  onMentionAsset?: (a: AssetResponse) => string
  canRewrite?: boolean
  error?: string
  help?: string
}

/** The description field: persistent label, counter, @ and # mentions, and a reviewed AI rewrite. */
export function PromptInput({ value, onChange, maxChars, label, characters, onMentionCharacter, onMentionAsset, canRewrite, error, help }: PromptInputProps) {
  const { t } = useTranslation('create')
  const toast = useToast()
  const listId = useId()
  const ref = useRef<HTMLTextAreaElement | null>(null)
  const [mention, setMention] = useState<Mention | null>(null)
  const [active, setActive] = useState(0)
  const [suggestion, setSuggestion] = useState<string | null>(null)

  const assets = useQuery({ queryKey: ['assets', 'mention-recent'], queryFn: () => createApi.recentAssets('image', 8), enabled: mention?.kind === '#' })
  const items =
    mention?.kind === '@'
      ? (characters ?? []).filter((c) => c.name.toLowerCase().includes(mention.query.toLowerCase())).slice(0, 8).map((c) => ({ id: c.biz_id, label: `@${c.name}`, character: c }))
      : mention?.kind === '#'
        ? (assets.data?.assets ?? []).map((a, i) => ({ id: a.biz_id, label: t('prompt.imageN', { n: i + 1 }), asset: a }))
        : []

  const rewrite = useMutation({
    mutationFn: () => createApi.rewrite(value),
    onSuccess: (res) => setSuggestion(res.text),
    onError: (err) => toast(`${t('rewrite.failed')} ${errorText(t, err)}`),
  })

  const sync = (el: HTMLTextAreaElement) => {
    const m = findMention(el.value, el.selectionStart)
    const enabled = m && ((m.kind === '@' && characters) || (m.kind === '#' && onMentionAsset))
    setMention(enabled ? m : null)
    setActive(0)
  }

  const pick = (i: number) => {
    const it = items[i]
    const el = ref.current
    if (!it || !mention || !el) return
    let token = it.label
    if ('character' in it && it.character) onMentionCharacter?.(it.character)
    if ('asset' in it && it.asset && onMentionAsset) token = onMentionAsset(it.asset)
    const before = value.slice(0, mention.start)
    const after = value.slice(mention.start + 1 + mention.query.length)
    const next = `${before}${token} ${after}`
    onChange(next)
    setMention(null)
    requestAnimationFrame(() => {
      const caret = before.length + token.length + 1
      el.focus()
      el.setSelectionRange(caret, caret)
    })
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (!mention || items.length === 0) return
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActive((a) => (a + 1) % items.length)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActive((a) => (a - 1 + items.length) % items.length)
    } else if (e.key === 'Enter' || e.key === 'Tab') {
      e.preventDefault()
      pick(active)
    } else if (e.key === 'Escape') {
      e.preventDefault()
      setMention(null)
    }
  }

  const open = mention !== null
  return (
    <div className="relative flex flex-col gap-1.5">
      <Field label={label} error={error} help={help ?? ((characters || onMentionAsset) && t('prompt.mentionHint'))}>
        <Textarea
          ref={ref}
          value={value}
          maxChars={maxChars}
          placeholder={t('prompt.placeholder')}
          onChange={(e) => {
            onChange(e.target.value)
            sync(e.target)
          }}
          onKeyDown={onKeyDown}
          onClick={(e) => sync(e.currentTarget)}
          onBlur={() => setTimeout(() => setMention(null), 150)}
          role="combobox"
          aria-autocomplete="list"
          aria-expanded={open}
          aria-controls={open ? listId : undefined}
          aria-activedescendant={open && items[active] ? `${listId}-${active}` : undefined}
          className="text-body-lg"
        />
      </Field>
      {canRewrite && (
        <Button
          size="sm"
          variant="ghost"
          icon={<Sparkles aria-hidden className="size-4" />}
          loading={rewrite.isPending}
          disabled={!value.trim()}
          onClick={() => rewrite.mutate()}
          className="absolute top-0 right-0"
        >
          {t('prompt.rewrite')}
        </Button>
      )}
      {open && (
        <ul
          id={listId}
          role="listbox"
          aria-label={mention.kind === '@' ? t('prompt.mentionCharacters') : t('prompt.mentionAssets')}
          className="absolute top-full right-0 left-0 z-30 mt-1 max-h-64 overflow-y-auto rounded-card border border-border bg-surface p-1 shadow-overlay"
        >
          {items.length === 0 ? (
            <li className="px-3 py-2 text-caption text-fg-muted">{t('prompt.mentionEmpty')}</li>
          ) : (
            items.map((it, i) => (
              <li
                key={it.id}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={i === active}
                onMouseDown={(e) => {
                  e.preventDefault()
                  pick(i)
                }}
                className={cn('flex cursor-pointer items-center gap-3 rounded-[8px] px-2.5 py-1.5 text-body', i === active ? 'bg-primary-soft text-primary-text' : 'text-fg')}
              >
                {'asset' in it && it.asset && <img src={it.asset.public_url} alt="" className="size-9 rounded-badge object-cover" />}
                {it.label}
              </li>
            ))
          )}
        </ul>
      )}
      <Dialog
        open={suggestion !== null}
        onOpenChange={(o) => !o && setSuggestion(null)}
        title={t('rewrite.title')}
        size="form"
        footer={
          <>
            <Button onClick={() => setSuggestion(null)}>{t('rewrite.keep')}</Button>
            <Button
              variant="primary"
              onClick={() => {
                if (suggestion !== null) onChange(suggestion)
                setSuggestion(null)
              }}
            >
              {t('rewrite.accept')}
            </Button>
          </>
        }
      >
        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <h3 className="mb-1 text-caption text-fg-muted">{t('rewrite.original')}</h3>
            <p className="rounded-card bg-surface-2 p-3 text-body whitespace-pre-wrap text-fg">{value}</p>
          </div>
          <div>
            <h3 className="mb-1 text-caption text-primary-text">{t('rewrite.suggestion')}</h3>
            <p className="rounded-card border border-primary bg-primary-soft p-3 text-body whitespace-pre-wrap text-fg">{suggestion}</p>
          </div>
        </div>
      </Dialog>
    </div>
  )
}
