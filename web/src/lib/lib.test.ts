import { describe, expect, it } from 'vitest'
import i18n from '../i18n'
import { ApiError } from './api/client'
import { errorText, failureText } from './errorText'
import { formatDuration } from './format'
import { resolveTheme } from './theme'

describe('resolveTheme', () => {
  it('follows the OS only for system', () => {
    expect(resolveTheme('light', true)).toBe('light')
    expect(resolveTheme('dark', false)).toBe('dark')
    expect(resolveTheme('system', true)).toBe('dark')
    expect(resolveTheme('system', false)).toBe('light')
  })
})

describe('formatDuration', () => {
  it('uses locale units and hides zero hours', async () => {
    await i18n.changeLanguage('en')
    expect(formatDuration(72_000, i18n.t)).toBe('1m 12s')
    expect(formatDuration(3_723_000, i18n.t)).toBe('1h 2m 3s')
    await i18n.changeLanguage('zh')
    expect(formatDuration(5_000, i18n.t)).toBe('5 秒')
  })
})

describe('errorText', () => {
  it('translates codes with params and never shows the server message', async () => {
    await i18n.changeLanguage('en')
    const err = new ApiError('image_size_unsupported', 'server english', 422, { allowed: ['1024x1024', '1536x1024'] })
    expect(errorText(i18n.t, err)).toBe('That size is not offered. Choose one of: 1024x1024, 1536x1024.')
    expect(errorText(i18n.t, new ApiError('no_such_code', 'secret detail', 500))).not.toContain('secret')
    expect(errorText(i18n.t, new TypeError('Failed to fetch'))).toMatch(/network/)
    await i18n.changeLanguage('zh')
    expect(errorText(i18n.t, new ApiError('references_too_many', 'x', 422, { max: 16 }))).toBe('参考图最多 16 张。')
  })

  it('maps failure codes and falls back for unknown ones', async () => {
    await i18n.changeLanguage('en')
    expect(failureText(i18n.t, 'moderation')).toMatch(/sensitive/)
    expect(failureText(i18n.t, 'something_new')).toBe(i18n.t('codes:unknown'))
  })
})
