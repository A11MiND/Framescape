import type { TFunction } from 'i18next'
import { ApiError } from './api/client'
import { UploadError } from './uploadError'

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
  if (err instanceof UploadError) {
    const cause = err.cause
    let reason: string
    if (cause instanceof TypeError || (cause instanceof ApiError && cause.status === 0)) reason = t('codes:network')
    else if (cause instanceof DOMException && cause.name === 'TimeoutError') reason = t('codes:upload.timeout')
    else if (cause instanceof ApiError && cause.code === 'upload_failed') {
      const key = cause.status === 413 ? 'tooLarge' : cause.status === 415 ? 'format' : cause.status === 401 || cause.status === 403 ? 'denied' : 'storage'
      reason = t(`codes:upload.${key}`, { status: cause.status })
    } else reason = errorText(t, cause)
    return t(`codes:upload.${err.stage}`, { reason })
  }
  if (err instanceof DOMException && err.name === 'AbortError') return t('codes:upload.cancelled')
  if (err instanceof DOMException && err.name === 'TimeoutError') return t('codes:upload.timeout')
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
