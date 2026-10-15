import { describe, expect, it } from 'vitest'
import { comicSize, fitComicCanvas, placeImage, resizeImage, withTopSpace, appendImage, constrainLayer, applyGeneratedImage, importPage, newComic, newLayer, parseComicDocument } from './comicDocument'

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

describe('extended canvas composition', () => {
  it('appends an image above native square, portrait and landscape comics', () => {
    for (const [page_width, page_height] of [[512, 512], [768, 1024], [1920, 1080]]) {
      const doc = { ...newComic('test'), page_width, page_height, layers: [newLayer('bubble', 'speech')] }
      const next = appendImage(doc, 'image', 'uploaded-image', 800, 140)
      const size = comicSize(next)
      expect(size.width).toBe(page_width)
      expect(size.comicHeight).toBe(page_height)
      expect(size.height).toBe(page_height + size.top)
      expect(next.layers[0].h * size.height).toBeCloseTo(doc.layers[0].h * page_height)
      expect(next.layers[0].y * size.height - size.top).toBeCloseTo(doc.layers[0].y * page_height)
      const image = next.layers[1]
      expect((image.y + image.h) * size.height).toBeLessThanOrEqual(size.top + .000001)
      expect(image.w * size.width / (image.h * size.height)).toBeCloseTo(800 / 140)
      expect(parseComicDocument(JSON.parse(JSON.stringify(next)))).toEqual(next)
    }
  })
  it('preserves pixel geometry on repeated extension, and rotation through backup and regeneration', () => {
    const doc = appendImage({ ...newComic(), page_width: 960, page_height: 640 }, 'one', 'asset', 800, 140)
    doc.layers[0].rotation = 12
    const next = withTopSpace(doc, doc.top_space! + 150)
    expect(next.layers[0].w * comicSize(next).width).toBeCloseTo(doc.layers[0].w * comicSize(doc).width)
    expect(next.layers[0].h * comicSize(next).height).toBeCloseTo(doc.layers[0].h * comicSize(doc).height)
    expect(parseComicDocument(JSON.parse(JSON.stringify(next)))).toEqual(next)
    expect(applyGeneratedImage(next, 'new', 0)).toMatchObject({ top_space: next.top_space, layers: next.layers })
    expect(() => parseComicDocument({ ...doc, top_space: -1 })).toThrow()
    expect(() => parseComicDocument({ ...doc, top_space: 1.5 })).toThrow()
    expect(() => parseComicDocument({ ...doc, page_height: undefined })).toThrow()
  })
  it('keeps rotated corners inside the whole canvas', () => {
    for (const [page_width, page_height] of [[512, 512], [768, 1024], [1920, 1080]]) for (const rotation of [-180, -135, -90, -45, 0, 30, 90, 180]) {
      const doc = { ...newComic(), page_width, page_height, top_space: 200 }
      const l = constrainLayer({ ...newLayer('logo', 'image'), rotation, x: .9, y: .95, w: .8, h: .7 }, doc)
      const size = comicSize(doc), angle = rotation * Math.PI / 180
      const halfW = (l.w * size.width * Math.abs(Math.cos(angle)) + l.h * size.height * Math.abs(Math.sin(angle))) / 2
      const halfH = (l.w * size.width * Math.abs(Math.sin(angle)) + l.h * size.height * Math.abs(Math.cos(angle))) / 2
      const cx = (l.x + l.w / 2) * size.width, cy = (l.y + l.h / 2) * size.height
      expect(cx - halfW).toBeGreaterThanOrEqual(-.00001)
      expect(cy - halfH).toBeGreaterThanOrEqual(-.00001)
      expect(cx + halfW).toBeLessThanOrEqual(size.width + .00001)
      expect(cy + halfH).toBeLessThanOrEqual(size.height + .00001)
    }
  })
})

describe('content-fitted canvas on every side', () => {
  for (const side of ['top', 'left', 'right', 'bottom'] as const) {
    it(`shrinks and grows ${side} space with the image without changing comic lettering`, () => {
      const base = { ...newComic(), page_width: 960, page_height: 640, layers: [newLayer('bubble', 'speech')] }
      const added = appendImage(base, 'logo', 'asset', 300, 120, side)
      const logo = added.layers[1]
      const smaller = fitComicCanvas({ ...added, layers: [added.layers[0], resizeImage(logo, 'w', logo.w / 2)] })
      const size = comicSize(smaller)
      expect(smaller[`${side}_space`]).toBeLessThan(added[`${side}_space`]!)
      expect(size.comicWidth).toBe(960); expect(size.comicHeight).toBe(640)
      expect(smaller.layers[0].x * size.width - size.left).toBeCloseTo(base.layers[0].x * 960)
      expect(smaller.layers[0].y * size.height - size.top).toBeCloseTo(base.layers[0].y * 640)
      expect(smaller.layers[0].w * size.width).toBeCloseTo(base.layers[0].w * 960)
      expect(smaller.layers[0].h * size.height).toBeCloseTo(base.layers[0].h * 640)
      expect(smaller.layers[1].w * size.width / (smaller.layers[1].h * size.height)).toBeCloseTo(2.5)
      const gap = fitComicCanvas({ ...smaller, image_gap: 20 })
      expect(gap[`${side}_space`]).toBe(smaller[`${side}_space`]! + 20)
      const rotated = fitComicCanvas({ ...smaller, layers: [smaller.layers[0], { ...smaller.layers[1], rotation: 45 }] })
      expect(parseComicDocument(JSON.parse(JSON.stringify(rotated)))).toEqual(rotated)
      const removed = fitComicCanvas({ ...rotated, layers: [rotated.layers[0]] })
      expect(comicSize(removed).width).toBe(960); expect(comicSize(removed).height).toBe(640)
      const hidden = fitComicCanvas({ ...added, layers: [added.layers[0], { ...logo, hidden: true }] })
      expect(hidden[`${side}_space`]).toBe(0)
      const shown = fitComicCanvas({ ...hidden, layers: hidden.layers.map(l => ({ ...l, hidden: false })) })
      expect(shown[`${side}_space`]).toBeGreaterThan(0)
    })
  }
  it('moves an existing image between sides and removes the previous empty strip', () => {
    let doc = appendImage({ ...newComic(), page_width: 512, page_height: 512 }, 'logo', 'asset', 150, 80)
    for (const side of ['right', 'bottom', 'left', 'top'] as const) {
      doc = placeImage(doc, doc.layers[0], side)
      expect(doc[`${side}_space`]).toBeGreaterThan(0)
      for (const other of ['right', 'bottom', 'left', 'top'] as const) if (other !== side) expect(doc[`${other}_space`]).toBe(0)
      expect(parseComicDocument(doc)).toEqual(doc)
    }
    doc = placeImage(doc, doc.layers[0], 'free')
    expect(comicSize(doc).width).toBe(512); expect(comicSize(doc).height).toBe(512)
  })
})
