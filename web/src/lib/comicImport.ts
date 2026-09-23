import { BACKGROUND_LIMIT, charCount } from './comicDocument'

export async function importComicSource(file: File): Promise<string> {
  if (file.size > 20 * 1024 * 1024) throw new Error('文件不能超过 20 MB')
  let text: string
  if (/\.pdf$/i.test(file.name)) {
    const pdfjs = await import('pdfjs-dist')
    pdfjs.GlobalWorkerOptions.workerSrc = new URL('pdfjs-dist/build/pdf.worker.min.mjs', import.meta.url).toString()
    const task = pdfjs.getDocument({ data: await file.arrayBuffer() })
    try {
      const pdf = await task.promise
      if (pdf.numPages > 300) throw new Error('PDF 不能超过 300 页')
      const pages: string[] = []; let length = 0
      for (let i = 1; i <= pdf.numPages; i++) {
        const page = await pdf.getPage(i)
        const content = await page.getTextContent()
        const pageText = content.items.map(item => 'str' in item ? item.str + ('hasEOL' in item && item.hasEOL ? '\n' : ' ') : '').join('')
        const section = `[第 ${i} 页]\n${pageText}`
        length += charCount(section) + 2
        if (length > BACKGROUND_LIMIT) throw new Error('文本超过 200,000 字符，请拆分文件；没有截断导入。')
        pages.push(section)
      }
      if (pages.every(page => page.replace(/\[第 \d+ 页\]/, '').trim().length < 10)) throw new Error('这是扫描版或没有可读取文字的 PDF，请先做 OCR 后再导入。')
      text = pages.join('\n\n')
    } finally { await task.destroy() }
  } else if (/\.(txt|md)$/i.test(file.name)) text = await file.text()
  else throw new Error('请使用 PDF、TXT 或 Markdown 文件')
  if (charCount(text) > BACKGROUND_LIMIT) throw new Error('文本超过 200,000 字符，请拆分文件；没有截断导入。')
  return text
}
