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
  rotation?: number
  placement?: ImagePlacement
  font_size: number
  color: string
  fill: string
  fill_opacity?: number
  tail: 'none' | 'left' | 'right'
  locked: boolean
  /** Kept in the document but neither shown nor exported. */
  hidden?: boolean
}
export interface ComicDocument {
  schema_version: 1
  /** Extra canvas space above the original page, in pixels. */
  top_space?: number
  left_space?: number
  right_space?: number
  bottom_space?: number
  auto_fit?: boolean
  image_gap?: number
  page_width?: number
  page_height?: number
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
  x: fraction, y: fraction, w: fraction.min(.0001), h: fraction.min(.0001),
  text: textLimit(2000), asset_id: z.string().optional(), font_size: z.number().int().min(12).max(96),
  color: z.string().regex(/^#[0-9a-fA-F]{6}$/), fill: z.string().regex(/^#[0-9a-fA-F]{6}$/),
  fill_opacity: fraction.optional(),
  placement: z.enum(['top', 'left', 'right', 'bottom', 'free']).optional(),
  rotation: z.number().finite().min(-180).max(180).optional(),
  tail: z.enum(['none', 'left', 'right']), locked: z.boolean(), hidden: z.boolean().optional(),
}).refine(l => l.x + l.w <= 1.000001 && l.y + l.h <= 1.000001 && (l.kind !== 'logo' || !!l.asset_id))
const documentSchema = z.object({
  page_width: z.number().int().positive().max(32768).optional(),
  page_height: z.number().int().positive().max(32768).optional(),
  auto_fit: z.boolean().optional(),
  image_gap: z.number().int().min(0).max(1000).optional(),
  left_space: z.number().int().min(0).max(32768).optional(),
  right_space: z.number().int().min(0).max(32768).optional(),
  bottom_space: z.number().int().min(0).max(32768).optional(),
  top_space: z.number().int().min(0).max(32768).optional(),
  schema_version: z.literal(1), title: textLimit(128).refine(s => s.trim().length > 0), mode: z.enum(['direct', 'editable']),
  brief: textLimit(20000), background: textLimit(200000), context: textLimit(8000),
  references: z.array(z.object({ asset_id: z.string().min(1), label: textLimit(200) })).max(MAX_REFERENCES),
  page_asset_id: z.string(), page_source: z.enum(['generated', 'imported']).optional(), panel_asset_ids: z.array(z.string()).length(4), layers: z.array(layerSchema).max(64),
  pending: z.object({ job_id: z.string().min(1), panel: z.number().int().min(0).max(4) }).optional(),
}).refine(d => (d.page_width === undefined) === (d.page_height === undefined))
.refine(d => new Set(d.layers.map(l => l.id)).size === d.layers.length)
export function parseComicDocument(value: unknown): ComicDocument {
  const result = documentSchema.safeParse(value)
  if (!result.success) throw new ComicError('invalidDocument')
  return result.data
}
/** A blank comic; callers pass the localized title. */
export function newComic(title = 'Untitled comic'): ComicDocument {
  return { schema_version: 1, auto_fit: true, title, mode: 'editable', brief: '', background: '', context: '', references: [], page_asset_id: '', panel_asset_ids: ['', '', '', ''], layers: [] }
}
export const charCount = (s: string) => Array.from(s).length
/** A new layer; callers pass the localized placeholder dialogue. */
export function newLayer(kind: ComicLayer['kind'], id: string, panel = 0, placeholder = ''): ComicLayer {
  return { id, kind, x: (panel % 2) * .5 + .035, y: Math.floor(panel / 2) * .5 + .035, w: kind === 'logo' ? .12 : .4, h: kind === 'logo' ? .12 : .16, text: kind === 'logo' ? '' : placeholder, font_size: 32, color: '#172033', fill: '#ffffff', tail: kind === 'bubble' ? 'left' : 'none', locked: false }
}
export function comicSize(doc: ComicDocument) {
  const comicWidth = doc.page_width ?? PAGE_WIDTH, comicHeight = doc.page_height ?? PAGE_HEIGHT
  const top = doc.top_space ?? 0, left = doc.left_space ?? 0, right = doc.right_space ?? 0, bottom = doc.bottom_space ?? 0
  return { width: comicWidth + left + right, height: comicHeight + top + bottom, top, left, right, bottom, comicWidth, comicHeight }
}
export function constrainLayer(l: ComicLayer, doc?: ComicDocument): ComicLayer {
  const { width, height } = doc ? comicSize(doc) : { width: PAGE_WIDTH, height: PAGE_HEIGHT }
  let w = Math.max(.0001, Math.min(1, l.w)), h = Math.max(.0001, Math.min(1, l.h))
  const angle = l.kind === 'logo' ? (l.rotation ?? 0) * Math.PI / 180 : 0
  const cos = Math.abs(Math.cos(angle)), sin = Math.abs(Math.sin(angle))
  const scale = Math.min(1, width / (w * width * cos + h * height * sin), height / (w * width * sin + h * height * cos))
  w = Math.max(.0001, w * scale); h = Math.max(.0001, h * scale)
  const halfW = (w * cos + h * height / width * sin) / 2
  const halfH = (w * width / height * sin + h * cos) / 2
  const cx = Math.max(halfW, Math.min(1 - halfW, l.x + w / 2))
  const cy = Math.max(halfH, Math.min(1 - halfH, l.y + h / 2))
  return { ...l, w, h, x: Math.max(0, Math.min(1 - w, cx - w / 2)), y: Math.max(0, Math.min(1 - h, cy - h / 2)) }
}
export type ImagePlacement = 'top' | 'left' | 'right' | 'bottom' | 'free'
type PixelLayer = { layer: ComicLayer; x: number; y: number; w: number; h: number }
function pixels(doc: ComicDocument, layer: ComicLayer): PixelLayer {
  const s = comicSize(doc)
  return { layer, x: layer.x * s.width - s.left, y: layer.y * s.height - s.top, w: layer.w * s.width, h: layer.h * s.height }
}
function bounds(p: PixelLayer) {
  const angle = (p.layer.kind === 'logo' ? p.layer.rotation ?? 0 : 0) * Math.PI / 180
  const w = Math.max(p.w, Math.abs(p.w * Math.cos(angle)) + Math.abs(p.h * Math.sin(angle)))
  const h = Math.max(p.h, Math.abs(p.w * Math.sin(angle)) + Math.abs(p.h * Math.cos(angle)))
  return { left: p.x + (p.w - w) / 2, top: p.y + (p.h - h) / 2, right: p.x + (p.w + w) / 2, bottom: p.y + (p.h + h) / 2 }
}
export function imagePlacement(doc: ComicDocument, layer: ComicLayer): ImagePlacement {
  if (layer.placement) return layer.placement
  const p = pixels(doc, layer), s = comicSize(doc)
  if (p.x + p.w / 2 < 0) return 'left'
  if (p.x + p.w / 2 > s.comicWidth) return 'right'
  if (p.y < 0) return 'top'
  if (p.y + p.h / 2 > s.comicHeight) return 'bottom'
  return 'free'
}
/** Fit the union of the original comic and image layers, keeping the comic at its native size. */
export function fitComicCanvas(doc: ComicDocument, preserveCanvas = false): ComicDocument {
  const s = comicSize(doc), gap = doc.image_gap ?? 0
  const all = doc.layers.map(l => pixels(doc, l))
  for (const side of ['top', 'bottom', 'left', 'right'] as const) {
    const group = all.filter(p => p.layer.kind === 'logo' && !p.layer.hidden && imagePlacement(doc, p.layer) === side)
    if (!group.length) continue
    const boxes = group.map(bounds)
    const dx = side === 'left' ? -gap - Math.max(...boxes.map(b => b.right)) : side === 'right' ? s.comicWidth + gap - Math.min(...boxes.map(b => b.left)) : 0
    const dy = side === 'top' ? -gap - Math.max(...boxes.map(b => b.bottom)) : side === 'bottom' ? s.comicHeight + gap - Math.min(...boxes.map(b => b.top)) : 0
    group.forEach(p => { p.x += dx; p.y += dy; p.layer = { ...p.layer, placement: side } })
  }
  const boxes = all.filter(p => !p.layer.hidden).map(bounds)
  const left = Math.max(preserveCanvas ? s.left : 0, Math.ceil(-Math.min(0, ...boxes.map(b => b.left)) - 1e-7))
  const top = Math.max(preserveCanvas ? s.top : 0, Math.ceil(-Math.min(0, ...boxes.map(b => b.top)) - 1e-7))
  const right = Math.max(preserveCanvas ? s.right : 0, Math.ceil(Math.max(s.comicWidth, ...boxes.map(b => b.right)) - s.comicWidth - 1e-7))
  const bottom = Math.max(preserveCanvas ? s.bottom : 0, Math.ceil(Math.max(s.comicHeight, ...boxes.map(b => b.bottom)) - s.comicHeight - 1e-7))
  const next = { ...doc, left_space: left, top_space: top, right_space: right, bottom_space: bottom }
  const total = comicSize(next)
  return { ...next, layers: all.map(p => {
    const layer = { ...p.layer, x: Math.max(0, p.x + left) / total.width, y: Math.max(0, p.y + top) / total.height, w: p.w / total.width, h: p.h / total.height }
    return layer.hidden ? constrainLayer(layer, next) : layer
  }) }
}
export function withCanvasSpace(doc: ComicDocument, side: 'top' | 'left' | 'right' | 'bottom', value: number): ComicDocument {
  const next = { ...doc, [`${side}_space`]: value, auto_fit: false }
  const total = comicSize(next)
  return { ...next, layers: doc.layers.map(l => {
    const p = pixels(doc, l)
    return constrainLayer({ ...l, x: (p.x + total.left) / total.width, y: (p.y + total.top) / total.height, w: p.w / total.width, h: p.h / total.height }, next)
  }) }
}
export const withTopSpace = (doc: ComicDocument, top: number) => withCanvasSpace(doc, 'top', top)
/** A position shortcut; the result stays an ordinary movable image layer. */
export function placeImage(doc: ComicDocument, layer: ComicLayer, placement: ImagePlacement): ComicDocument {
  const s = comicSize(doc), p = pixels(doc, layer), gap = doc.image_gap ?? 0
  const others = doc.layers.filter(l => l.id !== layer.id && l.kind === 'logo' && !l.hidden && imagePlacement(doc, l) === placement).map(l => bounds(pixels(doc, l)))
  p.x = (s.comicWidth - p.w) / 2; p.y = (s.comicHeight - p.h) / 2
  if (placement === 'top') p.y = Math.min(0, ...others.map(b => b.top)) - gap - p.h
  if (placement === 'bottom') p.y = Math.max(s.comicHeight, ...others.map(b => b.bottom)) + gap
  if (placement === 'left') p.x = Math.min(0, ...others.map(b => b.left)) - gap - p.w
  if (placement === 'right') p.x = Math.max(s.comicWidth, ...others.map(b => b.right)) + gap
  const placed = { ...layer, placement, x: (p.x + s.left) / s.width, y: (p.y + s.top) / s.height }
  const next = { ...doc, layers: doc.layers.map(l => l.id === layer.id ? placed : l) }
  return fitComicCanvas(next, doc.auto_fit === false)
}
export function appendImage(doc: ComicDocument, id: string, assetId: string, width: number, height: number, placement: ImagePlacement = 'top'): ComicDocument {
  const size = comicSize(doc), vertical = placement === 'top' || placement === 'bottom'
  const scale = Math.min(size.comicWidth * (vertical ? .8 : .4) / width, size.comicHeight * (vertical ? .4 : .8) / height, 1)
  const layer = { ...newLayer('logo', id), asset_id: assetId, w: Math.max(1, width * scale) / size.width, h: Math.max(1, height * scale) / size.height, rotation: 0, placement }
  return placeImage({ ...doc, auto_fit: doc.auto_fit ?? true, layers: [...doc.layers, layer] }, layer, placement)
}
/** Resize an image proportionally so its selection box never adds letterbox whitespace. */
export function resizeImage(layer: ComicLayer, key: 'w' | 'h', value: number): ComicLayer {
  const scale = Math.max(.0001, value) / layer[key]
  return { ...layer, w: layer.w * scale, h: layer.h * scale }
}
export function applyGeneratedImage(doc: ComicDocument, assetID: string, panel: number): ComicDocument {
  if (panel < 0 || panel > 4) throw new Error('Invalid panel')
  if (panel === 0) return { ...doc, page_asset_id: assetID, page_width: undefined, page_height: undefined, page_source: 'generated', panel_asset_ids: ['', '', '', ''], pending: undefined }
  const panels = [...doc.panel_asset_ids]; panels[panel - 1] = assetID
  return { ...doc, panel_asset_ids: panels, pending: undefined }
}
/** A page the user brought in; it replaces the generated one and its panel redraws. */
export function importPage(doc: ComicDocument, assetID: string): ComicDocument {
  return { ...doc, page_asset_id: assetID, page_width: undefined, page_height: undefined, page_source: 'imported', panel_asset_ids: ['', '', '', ''] }
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
