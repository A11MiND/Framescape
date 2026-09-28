import type { TFunction } from 'i18next'
import { ApiError } from './api/client'
import { ComicError } from './comicError'
import { UploadError } from './uploadError'
import { errorText } from './errorText'

/** The localized message for any comic editor failure. */
export function comicErrorText(t: TFunction, err: unknown, fallback = 'generic'): string {
  if (err instanceof ComicError) return t(`comic:error.${err.code}`, err.params)
  if (fallback !== 'generic') return t(`comic:error.${fallback}`)
  if (err instanceof ApiError || err instanceof TypeError || err instanceof UploadError || err instanceof DOMException) return errorText(t, err)
  return t(`comic:error.${fallback}`)
}
