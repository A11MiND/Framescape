// UI guard checks, run by `npm run lint`:
// 1. every locale namespace has the same keys in zh and en;
// 2. the codes namespace covers exactly the server's error code catalog
//    (src/i18n/error-codes.json, regenerate with `make error-codes`);
// 3. no emoji anywhere in src;
// 4. rebuilt code has no CJK literals (text lives in locale files) and no
//    raw palette classes (only semantic token utilities).
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..')
const src = path.join(root, 'src')
const problems = []

const readJSON = (p) => JSON.parse(fs.readFileSync(p, 'utf8'))
function flatKeys(obj, prefix = '') {
  return Object.entries(obj).flatMap(([k, v]) =>
    v && typeof v === 'object' && !Array.isArray(v) ? flatKeys(v, `${prefix}${k}.`) : [`${prefix}${k}`],
  )
}
function compare(label, a, b) {
  const ka = new Set(flatKeys(a))
  const kb = new Set(flatKeys(b))
  for (const k of ka) if (!kb.has(k)) problems.push(`${label}: "${k}" missing in en`)
  for (const k of kb) if (!ka.has(k)) problems.push(`${label}: "${k}" missing in zh`)
}

// 1. locale parity
const i18nDir = path.join(src, 'i18n')
compare('translation', readJSON(path.join(i18nDir, 'zh.json')), readJSON(path.join(i18nDir, 'en.json')))
const zhDir = path.join(i18nDir, 'locales', 'zh')
const enDir = path.join(i18nDir, 'locales', 'en')
const zhFiles = fs.readdirSync(zhDir).filter((f) => f.endsWith('.json')).sort()
const enFiles = fs.readdirSync(enDir).filter((f) => f.endsWith('.json')).sort()
if (zhFiles.join() !== enFiles.join()) problems.push(`namespaces differ: zh [${zhFiles}] en [${enFiles}]`)
for (const f of zhFiles.filter((f) => enFiles.includes(f))) {
  compare(f.replace('.json', ''), readJSON(path.join(zhDir, f)), readJSON(path.join(enDir, f)))
}

// 2. error code coverage
const catalog = readJSON(path.join(i18nDir, 'error-codes.json'))
const codes = readJSON(path.join(zhDir, 'codes.json'))
for (const section of ['api', 'failure']) {
  const want = new Set(Object.keys(catalog[section]))
  const have = new Set(Object.keys(codes[section] ?? {}))
  for (const k of want) if (!have.has(k)) problems.push(`codes.${section}: "${k}" has no text`)
  for (const k of have) if (!want.has(k)) problems.push(`codes.${section}: "${k}" is not in the server catalog`)
}

// 3 and 4. source scans
const EMOJI = /\p{Emoji_Presentation}|️/u
const CJK = /[　-〿㐀-鿿豈-﫿＀-￯]/u
const PALETTE =
  /\b(?:bg|text|border|ring|from|to|via|fill|stroke|outline|divide|placeholder|decoration|shadow|accent|caret)-(?:zinc|violet|neutral|gray|slate|stone|red|amber|emerald|green|blue|yellow|orange|purple|pink|indigo|sky|teal|cyan|lime|rose|fuchsia)-\d{2,3}\b/
const REBUILT = ['ui', 'app', 'features', 'lib/api', 'lib/stream', 'lib/format.ts', 'lib/errorText.ts', 'lib/theme.ts'].map((p) => path.join(src, p))
const isRebuilt = (p) => REBUILT.some((r) => p === r || p.startsWith(r + path.sep))
// Localized but not yet restyled: checked for CJK text only.
const LOCALIZED = ['pages/ComicStudio.tsx', 'components/ComicCanvas.tsx', 'lib/comicDocument.ts', 'lib/comicRender.ts', 'lib/comicImport.ts', 'lib/comicError.ts', 'lib/comicErrorText.ts'].map((p) => path.join(src, p))

function walk(dir) {
  for (const name of fs.readdirSync(dir)) {
    const p = path.join(dir, name)
    if (fs.statSync(p).isDirectory()) {
      walk(p)
      continue
    }
    if (!/\.(tsx?|css|json|html)$/.test(name)) continue
    const rel = path.relative(root, p)
    const lines = fs.readFileSync(p, 'utf8').split('\n')
    const source = /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)
    const code = source && isRebuilt(p)
    const localized = source && LOCALIZED.includes(p)
    lines.forEach((line, i) => {
      const at = `${rel}:${i + 1}`
      if (EMOJI.test(line)) problems.push(`${at}: emoji`)
      if ((code || localized) && CJK.test(line)) problems.push(`${at}: CJK text belongs in a locale file`)
      if (code && PALETTE.test(line)) problems.push(`${at}: raw palette class, use a semantic token`)
    })
  }
}
walk(src)

if (problems.length) {
  console.error(problems.map((p) => `  ${p}`).join('\n'))
  console.error(`check-ui: ${problems.length} problem(s)`)
  process.exit(1)
}
console.log('check-ui: ok')
