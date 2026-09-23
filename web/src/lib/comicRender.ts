import { PAGE_WIDTH as W, PAGE_HEIGHT as H, wrapText, type ComicDocument, type ComicLayer } from './comicDocument'

export type ComicImages = Record<string, HTMLImageElement>
export function loadComicImage(url: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const image = new Image(); image.crossOrigin = 'anonymous'
    image.onload = () => resolve(image)
    image.onerror = () => reject(new Error('图片加载失败。请检查素材链接及存储服务的跨域设置。'))
    image.src = url
  })
}
function contain(ctx: CanvasRenderingContext2D, image: HTMLImageElement, x: number, y: number, w: number, h: number) {
  const scale = Math.min(w / image.naturalWidth, h / image.naturalHeight)
  const dw = image.naturalWidth * scale, dh = image.naturalHeight * scale
  ctx.drawImage(image, x + (w - dw) / 2, y + (h - dh) / 2, dw, dh)
}
// One renderer is used for the preview and export. No DOM screenshot, remote
// SVG, foreignObject, or separate text-wrapping implementation is involved.
export function renderComic(canvas: HTMLCanvasElement, doc: ComicDocument, images: ComicImages): string[] {
  canvas.width = W; canvas.height = H
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('浏览器不支持画布')
  ctx.fillStyle = '#ffffff'; ctx.fillRect(0, 0, W, H)
  if (images[doc.page_asset_id]) contain(ctx, images[doc.page_asset_id], 0, 0, W, H)
  else {
    ctx.fillStyle = '#f1f5f9'; ctx.fillRect(0, 0, W, H)
    ctx.fillStyle = '#64748b'; ctx.font = '28px sans-serif'; ctx.textAlign = 'center'
    for (let i = 0; i < 4; i++) ctx.fillText(`第 ${i + 1} 格`, (i % 2) * W / 2 + W / 4, Math.floor(i / 2) * H / 2 + H / 4)
  }
  doc.panel_asset_ids.forEach((id, i) => {
    if (!images[id]) return
    const x = (i % 2) * W / 2, y = Math.floor(i / 2) * H / 2
    ctx.fillStyle = '#ffffff'; ctx.fillRect(x, y, W / 2, H / 2)
    contain(ctx, images[id], x, y, W / 2, H / 2)
  })
  if (doc.mode === 'editable') {
    ctx.strokeStyle = '#172033'; ctx.lineWidth = 4; ctx.strokeRect(2, 2, W - 4, H - 4)
    ctx.beginPath(); ctx.moveTo(W / 2, 0); ctx.lineTo(W / 2, H); ctx.moveTo(0, H / 2); ctx.lineTo(W, H / 2); ctx.stroke()
  }
  const overflow: string[] = []
  for (const layer of doc.layers) {
    if (layer.kind === 'logo') {
      if (images[layer.asset_id ?? '']) contain(ctx, images[layer.asset_id!], layer.x * W, layer.y * H, layer.w * W, layer.h * H)
    } else if (drawTextLayer(ctx, layer)) overflow.push(layer.id)
  }
  return overflow
}
function drawTextLayer(ctx: CanvasRenderingContext2D, l: ComicLayer): boolean {
  const x = l.x * W, y = l.y * H, w = l.w * W, h = l.h * H
  const tail = l.kind === 'bubble' && l.tail !== 'none' ? Math.min(24, h * .2) : 0
  const bodyH = h - tail, pad = 18
  ctx.save(); ctx.fillStyle = l.fill; ctx.strokeStyle = l.color; ctx.lineWidth = 3
  if (l.kind === 'bubble') {
    ctx.beginPath(); ctx.roundRect(x, y, w, bodyH, Math.min(22, bodyH / 3)); ctx.fill(); ctx.stroke()
    if (tail) {
      const tx = x + w * (l.tail === 'left' ? .25 : .75)
      ctx.beginPath(); ctx.moveTo(tx - 14, y + bodyH - 2); ctx.lineTo(tx + (l.tail === 'left' ? -22 : 22), y + h); ctx.lineTo(tx + 14, y + bodyH - 2); ctx.fill(); ctx.stroke()
      ctx.fillRect(tx - 12, y + bodyH - 4, 24, 6)
    }
  }
  ctx.fillStyle = l.color; ctx.font = `${l.font_size}px "Noto Sans TC", "PingFang TC", "Microsoft JhengHei", sans-serif`
  ctx.textAlign = 'left'; ctx.textBaseline = 'top'
  const lines = wrapText(l.text, Math.max(1, w - pad * 2), text => ctx.measureText(text).width)
  const lineHeight = l.font_size * 1.3
  const overflow = lines.length * lineHeight > bodyH - pad * 2
  ctx.beginPath(); ctx.rect(x + pad, y + pad, Math.max(0, w - pad * 2), Math.max(0, bodyH - pad * 2)); ctx.clip()
  lines.forEach((line, i) => ctx.fillText(line, x + pad, y + pad + lineHeight * i))
  ctx.restore()
  return overflow
}
export async function exportComic(doc: ComicDocument, images: ComicImages): Promise<Blob> {
  await document.fonts.load('32px "Noto Sans TC"', doc.layers.map(l => l.text).join('') || '漫画')
  await document.fonts.ready
  const canvas = document.createElement('canvas')
  if (renderComic(canvas, doc, images).length) throw new Error('对白超出对话框，请放大对话框或减小字号后再导出。')
  for (const id of [doc.page_asset_id, ...doc.panel_asset_ids, ...doc.layers.map(l => l.asset_id ?? '')].filter(Boolean)) {
    if (!images[id]) throw new Error('仍有图片未加载，暂时不能导出。')
  }
  return new Promise((resolve, reject) => {
    try { canvas.toBlob(blob => blob ? resolve(blob) : reject(new Error('无法导出图片')), 'image/png') }
    catch { reject(new Error('图片跨域设置阻止导出，请检查存储服务 CORS。')) }
  })
}
