import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { OpenAICapabilities } from '../../lib/api/create'

function gcd(a: number, b: number): number {
  return b ? gcd(b, a % b) : a
}

export function useSizeLabel() {
  const { t } = useTranslation('create')
  return (size: string) => {
    const [w, h] = size.split('x').map(Number)
    if (!w || !h) return size
    const g = gcd(w, h)
    const shape = w === h ? 'square' : w > h ? 'landscape' : 'portrait'
    return `${t(`gpt.shape.${shape}`)} ${w / g}:${h / g}`
  }
}

/** Keeps a stored option only while the deployment still offers it. */
export function pickOption(stored: string, offered: string[], fallback: string) {
  if (stored && offered.includes(stored)) return { value: stored, gone: false }
  return { value: offered.includes(fallback) ? fallback : (offered[0] ?? ''), gone: Boolean(stored) }
}


/**
 * The size and quality to send. Options come only from the deployment; a
 * stored value it no longer offers is replaced (and so repriced) with a notice.
 */
export function useGptChoices(openai: OpenAICapabilities | undefined, stored: { size: string; quality: string }, save: (patch: { size: string; quality: string }) => void) {
  const { t } = useTranslation('create')
  const [gone, setGone] = useState<string | null>(null)
  const size = pickOption(stored.size, openai?.sizes ?? [], '1024x1024')
  const quality = pickOption(stored.quality, openai?.qualities ?? [], openai?.default_quality ?? 'high')
  useEffect(() => {
    if (!openai) return
    const changed = [size.gone && [stored.size, size.value], quality.gone && [stored.quality, quality.value]].filter(Boolean) as string[][]
    if (changed.length) {
      setGone(t('gpt.optionGone', { value: changed[0][0], next: changed[0][1] }))
      save({ size: size.value, quality: quality.value })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openai, size.gone, quality.gone])
  return { size, quality, gone }
}
