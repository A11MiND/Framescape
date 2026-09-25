import { Link, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { BookOpen, Clapperboard, Image as ImageIcon } from 'lucide-react'
import { cn } from '../../ui'
import { createApi } from '../../lib/api/create'
import { keys } from '../../lib/api/keys'
import { hasOpenAIImage } from '../../lib/api/account'
import { useMe } from '../../app/useMe'
import type { CreateMode } from '../../app/routing'

const FAMILIES = [
  { key: 'image', icon: ImageIcon, modes: ['image', 'image-sequence', 'gpt'] },
  { key: 'comic', icon: BookOpen, modes: ['comic', 'comic-classic'] },
  { key: 'video', icon: Clapperboard, modes: ['video', 'video-sequence'] },
] as const

/** Two-level creation mode navigation: image / comic / video, then the modes of each. */
export function CreateNav({ mode }: { mode: CreateMode }) {
  const { t } = useTranslation('create')
  const { search } = useLocation()
  const me = useMe()
  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities, staleTime: 60_000 })
  const family = FAMILIES.find((f) => (f.modes as readonly string[]).includes(mode)) ?? FAMILIES[0]
  const gptBadge = !caps.data?.providers?.openai.enabled ? t('nav.badge.unavailable') : me.data && !hasOpenAIImage(me.data) ? t('nav.badge.beta') : null

  return (
    <nav aria-label={t('nav.label')} className="flex flex-col gap-2">
      <ul className="flex gap-1 border-b border-border">
        {FAMILIES.map((f) => {
          const current = f.key === family.key
          return (
            <li key={f.key}>
              <Link
                to={{ pathname: `/create/${f.modes[0]}`, search }}
                aria-current={current ? 'page' : undefined}
                className={cn(
                  '-mb-px inline-flex h-12 items-center gap-2 border-b-2 px-4 text-[15px] font-medium transition-colors',
                  current ? 'border-primary text-primary-text' : 'border-transparent text-fg-muted hover:text-fg',
                )}
              >
                <f.icon aria-hidden className="size-5" />
                {t(`nav.family.${f.key}`)}
              </Link>
            </li>
          )
        })}
      </ul>
      <ul className="flex flex-wrap gap-2">
        {family.modes.map((m) => {
          const current = m === mode
          return (
            <li key={m}>
              <Link
                to={`/create/${m}`}
                aria-current={current ? 'page' : undefined}
                className={cn(
                  'inline-flex h-9 items-center gap-2 rounded-full border px-3.5 text-body font-medium transition-colors',
                  current ? 'border-primary bg-primary-soft text-primary-text' : 'border-border-control bg-surface text-fg hover:bg-surface-2',
                )}
              >
                {t(`nav.mode.${m}`)}
                {m === 'gpt' && gptBadge && <span className="rounded-full bg-surface-2 px-2 text-badge text-fg-muted">{gptBadge}</span>}
              </Link>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}
