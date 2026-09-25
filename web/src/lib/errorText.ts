import type { TFunction } from 'i18next'
import { ApiError } from './api/client'

function params(raw: Record<string, unknown>, sep: string): Record<string, string | number> {
  const out: Record<string, string | number> = {}
  for (const [k, v] of Object.entries(raw)) {
    if (Array.isArray(v)) out[k] = v.join(sep)
    else if (typeof v === 'number' || typeof v === 'string') out[k] = v
  }
  return out
}

/**
 * The localized message for any error. API errors are translated by code;
 * the server's English message is never shown.
 */
export function errorText(t: TFunction, err: unknown): string {
  if (err instanceof ApiError) {
    const key = `codes:api.${err.code}`
    const text = t(key, params(err.params, t('ui:listSeparator')))
    if (text !== key && text !== `api.${err.code}`) return text
    return t('codes:unknown')
  }
  if (err instanceof TypeError) return t('codes:network')
  return t('codes:unknown')
}

/** The localized reason of a failed task or step, by failure code. */
export function failureText(t: TFunction, code: string | undefined | null): string {
  if (!code) return t('codes:unknown')
  const key = `codes:failure.${code}`
  const text = t(key)
  return text === key || text === `failure.${code}` ? t('codes:unknown') : text
}
