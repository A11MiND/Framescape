import { afterEach, describe, expect, it } from 'vitest'
import i18n from './index'

describe('English count copy', () => {
  afterEach(() => {
    i18n.changeLanguage('zh')
  })

  it('uses singular for one and plural for zero and two', () => {
    i18n.changeLanguage('en')
    const t = i18n.getFixedT('en', 'library')
    expect(t('delete.title', { n: 0, count: 0 })).toBe('Delete 0 items?')
    expect(t('delete.title', { n: 1, count: 1 })).toBe('Delete 1 item?')
    expect(t('delete.title', { n: 2, count: 2 })).toBe('Delete 2 items?')
  })

  it('keeps singular nouns in the creation and task summaries', () => {
    i18n.changeLanguage('en')
    expect(i18n.t('sequence.count', { ns: 'create', n: 1, count: 1 })).toBe('1 image')
    expect(i18n.t('sequence.count', { ns: 'create', n: 2, count: 2 })).toBe('2 images')
    expect(i18n.t('results.count', { ns: 'job', n: 1, count: 1 })).toBe('1 result')
  })
})
