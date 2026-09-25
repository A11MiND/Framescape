import type { TFunction } from 'i18next'

// Locale-aware formatting shared by the UI kit and pages.

/** A duration in the locale's units ("1m 12s"); hours appear only when non-zero. */
export function formatDuration(ms: number, t: TFunction): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const parts: string[] = []
  if (h) parts.push(t('ui:unit.hours', { n: h }))
  if (h || m) parts.push(t('ui:unit.minutes', { n: m }))
  parts.push(t('ui:unit.seconds', { n: s }))
  return parts.join(' ')
}

/** Wall-clock time with seconds, e.g. 14:20:05. */
export function formatClock(d: Date, lang: string): string {
  return d.toLocaleTimeString(lang.startsWith('zh') ? 'zh-CN' : 'en-US', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
}

/** Date and time without seconds in the locale's format. */
export function formatDateTime(d: Date | string, lang: string): string {
  return new Date(d).toLocaleString(lang.startsWith('zh') ? 'zh-CN' : 'en-US', {
    year: 'numeric',
    month: lang.startsWith('zh') ? 'long' : 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}

/** An integer with grouping, e.g. 1,240. */
export function formatNumber(n: number, lang: string): string {
  return n.toLocaleString(lang.startsWith('zh') ? 'zh-CN' : 'en-US')
}
