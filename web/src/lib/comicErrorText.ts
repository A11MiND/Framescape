import type { TFunction } from 'i18next'
import { ApiError } from './api/client'
import { ComicError } from './comicError'
import { errorText } from './errorText'

/** The localized message for any comic editor failure. */
export function comicErrorText(t: TFunction, err: unknown): string {
  if (err instanceof ComicError) return t(`comic:error.${err.code}`, err.params)
  if (err instanceof ApiError || err instanceof TypeError) return errorText(t, err)
  return t('comic:error.generic')
}
