import { BACKGROUND_LIMIT, charCount } from './comicDocument'
import { ComicError } from './comicError'

/** Reads a PDF (text layer), TXT or Markdown source; `pageLabel` marks each PDF page for provenance. */
export async function importComicSource(file: File, pageLabel: (page: number) => string): Promise<string> {
  if (file.size > 20 * 1024 * 1024) throw new ComicError('fileTooLarge', { mb: 20 })
  let text: string
  if (/\.pdf$/i.test(file.name)) {
    const pdfjs = await import('pdfjs-dist')
    pdfjs.GlobalWorkerOptions.workerSrc = new URL('pdfjs-dist/build/pdf.worker.min.mjs', import.meta.url).toString()
    const task = pdfjs.getDocument({ data: await file.arrayBuffer() })
    try {
      const pdf = await task.promise
      if (pdf.numPages > 300) throw new ComicError('pdfTooManyPages', { max: 300 })
      const pages: string[] = []; const bodies: string[] = []; let length = 0
      for (let i = 1; i <= pdf.numPages; i++) {
        const page = await pdf.getPage(i)
        const content = await page.getTextContent()
        const pageText = content.items.map(item => 'str' in item ? item.str + ('hasEOL' in item && item.hasEOL ? '\n' : ' ') : '').join('')
        const section = `${pageLabel(i)}\n${pageText}`
        length += charCount(section) + 2
        if (length > BACKGROUND_LIMIT) throw new ComicError('textTooLong', { max: BACKGROUND_LIMIT })
        pages.push(section); bodies.push(pageText)
      }
      if (bodies.every(body => body.trim().length < 10)) throw new ComicError('scannedPdf')
      text = pages.join('\n\n')
    } finally { await task.destroy() }
  } else if (/\.(txt|md)$/i.test(file.name)) text = await file.text()
  else throw new ComicError('unsupportedFile')
  if (charCount(text) > BACKGROUND_LIMIT) throw new ComicError('textTooLong', { max: BACKGROUND_LIMIT })
  return text
}
