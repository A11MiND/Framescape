import { ComicError } from './comicError'
import { z } from 'zod'

export const PAGE_WIDTH = 1536
export const PAGE_HEIGHT = 1024
export const BACKGROUND_LIMIT = 200_000
// Mirrors domain/comic.MaxReferences (OpenAI's 16-image cap minus the page
// a single-panel redraw also sends) and comic.ImageMime.
export const MAX_REFERENCES = 15
export const COMIC_IMAGE_MIMES = ['image/png', 'image/jpeg', 'image/webp']
export interface ComicLayer {
  id: string
  kind: 'bubble' | 'text' | 'logo'
  x: number; y: number; w: number; h: number
  text: string
  asset_id?: string
  font_size: number
  color: string
  fill: string
  tail: 'none' | 'left' | 'right'
  locked: boolean
  /** Kept in the document but neither shown nor exported. */
  hidden?: boolean
}
export interface ComicDocument {
  schema_version: 1
  title: string
  mode: 'editable' | 'direct'
  brief: string
  background: string
  context: string
  references: { asset_id: string; label: string }[]
  page_asset_id: string
  /** Where the page came from; absent in drafts saved before it existed. */
  page_source?: 'generated' | 'imported'
  panel_asset_ids: string[]
  layers: ComicLayer[]
  pending?: { job_id: string; panel: number }
}
export interface SavedComic { biz_id: string; version: number; document: ComicDocument }
export interface ComicSummary { biz_id: string; title: string; version: number }
// Untrusted local/backup JSON is validated before rendering; never import URLs,
// arbitrary markup, or unknown layer types from a portable edit document.
const fraction = z.number().finite().min(0).max(1)
const textLimit = (max: number) => z.string().refine(s => Array.from(s).length <= max)
const layerSchema = z.object({
  id: z.string().min(1).max(80), kind: z.enum(['bubble', 'text', 'logo']),
  x: fraction, y: fraction, w: fraction.min(.02), h: fraction.min(.02),
  text: textLimit(2000), asset_id: z.string().optional(), font_size: z.number().int().min(12).max(96),
  color: z.string().regex(/^#[0-9a-fA-F]{6}$/), fill: z.string().regex(/^#[0-9a-fA-F]{6}$/),
  tail: z.enum(['none', 'left', 'right']), locked: z.boolean(), hidden: z.boolean().optional(),
}).refine(l => l.x + l.w <= 1.000001 && l.y + l.h <= 1.000001 && (l.kind !== 'logo' || !!l.asset_id))
const documentSchema = z.object({
  schema_version: z.literal(1), title: textLimit(128).refine(s => s.trim().length > 0), mode: z.enum(['direct', 'editable']),
  brief: textLimit(20000), background: textLimit(200000), context: textLimit(8000),
  references: z.array(z.object({ asset_id: z.string().min(1), label: textLimit(200) })).max(MAX_REFERENCES),
  page_asset_id: z.string(), page_source: z.enum(['generated', 'imported']).optional(), panel_asset_ids: z.array(z.string()).length(4), layers: z.array(layerSchema).max(64),
  pending: z.object({ job_id: z.string().min(1), panel: z.number().int().min(0).max(4) }).optional(),
}).refine(d => new Set(d.layers.map(l => l.id)).size === d.layers.length)
export function parseComicDocument(value: unknown): ComicDocument {
  const result = documentSchema.safeParse(value)
  if (!result.success) throw new ComicError('invalidDocument')
  return result.data
}
/** A blank comic; callers pass the localized title. */
export function newComic(title = 'Untitled comic'): ComicDocument {
  return { schema_version: 1, title, mode: 'editable', brief: '', background: '', context: '', references: [], page_asset_id: '', panel_asset_ids: ['', '', '', ''], layers: [] }
}
export const charCount = (s: string) => Array.from(s).length
/** A new layer; callers pass the localized placeholder dialogue. */
export function newLayer(kind: ComicLayer['kind'], id: string, panel = 0, placeholder = ''): ComicLayer {
  return { id, kind, x: (panel % 2) * .5 + .035, y: Math.floor(panel / 2) * .5 + .035, w: kind === 'logo' ? .12 : .4, h: kind === 'logo' ? .12 : .16, text: kind === 'logo' ? '' : placeholder, font_size: 32, color: '#172033', fill: '#ffffff', tail: kind === 'bubble' ? 'left' : 'none', locked: false }
}
export function constrainLayer(l: ComicLayer): ComicLayer {
  const w = Math.max(.02, Math.min(1, l.w)), h = Math.max(.02, Math.min(1, l.h))
  return { ...l, w, h, x: Math.max(0, Math.min(1 - w, l.x)), y: Math.max(0, Math.min(1 - h, l.y)) }
}
export function applyGeneratedImage(doc: ComicDocument, assetID: string, panel: number): ComicDocument {
  if (panel < 0 || panel > 4) throw new Error('Invalid panel')
  if (panel === 0) return { ...doc, page_asset_id: assetID, page_source: 'generated', panel_asset_ids: ['', '', '', ''], pending: undefined }
  const panels = [...doc.panel_asset_ids]; panels[panel - 1] = assetID
  return { ...doc, panel_asset_ids: panels, pending: undefined }
}
/** A page the user brought in; it replaces the generated one and its panel redraws. */
export function importPage(doc: ComicDocument, assetID: string): ComicDocument {
  return { ...doc, page_asset_id: assetID, page_source: 'imported', panel_asset_ids: ['', '', '', ''] }
}
// Source retrieval is local and reviewable, not an LLM summary. Never send the
// entire 200k source to an image model or silently cut off the approved brief.
/** `rangeLabel` marks each excerpt with its position in the source for review. */
export function sourceExcerpts(source: string, query: string, rangeLabel: (from: number, to: number) => string, limit = 6500): string {
  const chars = Array.from(source)
  const terms = Array.from(new Set(query.toLowerCase().match(/[a-z0-9]{2,}|[\u3400-\u9fff]{2,}/g) ?? []))
    .flatMap(t => /[\u3400-\u9fff]/.test(t) ? Array.from(t).slice(0, -1).map((_, i) => t.slice(i, i + 2)) : [t])
  const chunks: { text: string; start: number; score: number }[] = []
  for (let start = 0; start < chars.length; start += 900) {
    const text = chars.slice(start, start + 900).join('')
    chunks.push({ text, start, score: terms.reduce((score, term) => score + (text.toLowerCase().includes(term) ? 1 : 0), 0) })
  }
  let length = 0
  return chunks.sort((a, b) => b.score - a.score || a.start - b.start).slice(0, 7).sort((a, b) => a.start - b.start)
    .map(chunk => `${rangeLabel(chunk.start + 1, chunk.start + charCount(chunk.text))}\n${chunk.text}`)
    .filter(text => { length += charCount(text) + 2; return length <= limit }).join('\n\n')
}
export function wrapText(text: string, width: number, measure: (text: string) => number): string[] {
  const lines: string[] = []
  for (const paragraph of text.split('\n')) {
    let line = ''
    for (const char of Array.from(paragraph)) {
      if (line && measure(line + char) > width) { lines.push(line); line = char } else line += char
    }
    lines.push(line)
  }
  return lines
}
