import { test } from 'node:test'
import assert from 'node:assert/strict'
import { newComic, newLayer, constrainLayer, applyGeneratedImage, sourceExcerpts, charCount, wrapText, parseComicDocument } from '../src/lib/comicDocument.ts'

test('replacement preserves all dialogue, logo and other panels', () => {
  const doc = newComic(); doc.page_asset_id = 'original'; doc.layers = [newLayer('bubble', 'one'), { ...newLayer('logo', 'logo'), asset_id: 'brand-logo' }]
  doc.panel_asset_ids = ['a', '', 'c', '']; doc.pending = { job_id: 'job', panel: 2 }
  const next = applyGeneratedImage(doc, 'replacement', 2)
  assert.deepEqual(next.panel_asset_ids, ['a', 'replacement', 'c', ''])
  assert.deepEqual(next.layers, doc.layers); assert.equal(next.pending, undefined); assert.equal(doc.panel_asset_ids[1], '')
})
test('whole-page replacement clears old patches but preserves editable overlays', () => {
  const doc = newComic(); doc.layers = [newLayer('bubble', 'one')]; doc.panel_asset_ids[0] = 'patch'
  const next = applyGeneratedImage(doc, 'page', 0)
  assert.deepEqual(next.panel_asset_ids, ['', '', '', '']); assert.equal(next.layers, doc.layers)
})
test('drag and resize keep layers inside page', () => {
  const next = constrainLayer({ ...newLayer('bubble', 'one'), x: 2, y: -.2, w: 1.2, h: -.1 })
  assert.equal(next.x, 0); assert.equal(next.y, 0); assert.equal(next.w, 1); assert.equal(next.h, .02)
})
test('Chinese and supplementary characters are not split by UTF16 units', () => {
  assert.equal(charCount('智𠮷A'), 3)
  assert.deepEqual(wrapText('智𠮷A\n第二行', 2, s => Array.from(s).length), ['智𠮷', 'A', '第二', '行'])
})
test('200k background retrieves relevant excerpts with source coordinates', () => {
  const source = '普通内容。'.repeat(199_000 / 5) + '2050绿色能源转型，港燈工程師到社区探访长者。'
  const excerpt = sourceExcerpts(source, '社区探访长者')
  assert.ok(excerpt.includes('社区探访长者')); assert.ok(excerpt.includes('[原文字符')); assert.ok(charCount(excerpt) <= 6500)
  assert.equal(sourceExcerpts('', 'story'), '')
})
test('invalid panel indices are rejected', () => assert.throws(() => applyGeneratedImage(newComic(), 'x', 5)))
test('imported editable JSON is validated before rendering', () => {
  assert.deepEqual(parseComicDocument(newComic()), newComic())
  for (const invalid of [null, {}, { ...newComic(), background: 'a'.repeat(200001) }, { ...newComic(), layers: [{ ...newLayer('bubble', 'a'), fill: 'url(javascript:1)' }] }, { ...newComic(), panel_asset_ids: [] }]) assert.throws(() => parseComicDocument(invalid))
})
