import { describe, expect, it } from 'vitest'
import { applyGeneratedImage, importPage, newComic, newLayer, parseComicDocument } from './comicDocument'

describe('comic document', () => {
  it('records whether the page was generated or imported', () => {
    const generated = applyGeneratedImage(newComic('t'), 'gen', 0)
    expect(generated.page_source).toBe('generated')
    const panel = applyGeneratedImage(generated, 'p2', 2)
    expect(panel.page_source).toBe('generated')
    expect(panel.panel_asset_ids).toEqual(['', 'p2', '', ''])
    const imported = importPage(panel, 'upload')
    expect(imported).toMatchObject({ page_asset_id: 'upload', page_source: 'imported', panel_asset_ids: ['', '', '', ''] })
  })

  it('keeps hidden layers and the page source through a backup round trip', () => {
    const doc = { ...importPage(newComic('t'), 'upload'), layers: [{ ...newLayer('text', 'l1', 0, 'hi'), hidden: true }] }
    const parsed = parseComicDocument(JSON.parse(JSON.stringify(doc)))
    expect(parsed.layers[0].hidden).toBe(true)
    expect(parsed.page_source).toBe('imported')
    expect(() => parseComicDocument({ ...doc, page_source: 'gpt' })).toThrow()
  })
})
