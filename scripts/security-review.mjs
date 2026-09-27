#!/usr/bin/env node
// Small dependency-free checks for the phase 7 local security gate. This is
// deliberately a review aid, not a replacement for penetration testing.
import { execFileSync } from 'node:child_process'
import { readdir, readFile } from 'node:fs/promises'
import { join, relative } from 'node:path'

const root = new URL('..', import.meta.url).pathname
const roots = ['internal', 'cmd', 'web/src'].map((dir) => join(root, dir))
const sourceExt = /\.(go|ts|tsx|js|mjs)$/
const findings = []
const files = []

async function walk(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) await walk(path)
    else if (sourceExt.test(entry.name)) files.push(path)
  }
}
for (const dir of roots) await walk(dir)

for (const path of files) {
  const text = await readFile(path, 'utf8')
  const name = relative(root, path)
  // These APIs bypass React escaping or create code from strings. A finding
  // is actionable even when it occurs in a test, so do not exclude tests.
  if (/dangerouslySetInnerHTML|\beval\s*\(|new\s+Function\s*\(|innerHTML\s*=/.test(text)) {
    findings.push(`${name}: dynamic HTML/code API`)
  }
}

const router = await readFile(join(root, 'internal/interfaces/http/server.go'), 'utf8')
if (!/authed\.Use\(s\.requireAuth\(\)\)/.test(router)) findings.push('server.go: authenticated route group is not protected')
if (!/admin\.Use\(s\.requireAdmin\(\)\)/.test(router)) findings.push('server.go: admin route group is not protected')
if (!/allowAll \|\| slices\.Contains\(allowed, origin\)/.test(router) || !/config\.Env\(\) != "prod"/.test(router)) {
  findings.push('server.go: CORS production guard is missing or permissive')
}
const jwt = await readFile(join(root, 'internal/interfaces/http/jwtauth.go'), 'utf8')
if (!/SigningMethodHMAC/.test(jwt) || !/c\.Type != want/.test(jwt)) findings.push('jwtauth.go: token algorithm/type checks are incomplete')

// Secrets should arrive through environment/configuration, never from a
// tracked file. Test fixtures may contain the word "secret", so only inspect
// obvious long assignment literals in non-test source.
for (const path of files.filter((p) => !p.endsWith('_test.go') && !p.endsWith('.test.ts'))) {
  const text = await readFile(path, 'utf8')
  if (/(?:OPENAI|MINIMAX|GEMINI|JWT)_?(?:API_?KEY|SECRET)\s*[:=]\s*["'][^"']{16,}["']/.test(text)) {
    findings.push(`${relative(root, path)}: possible hard-coded provider secret`)
  }
}

if (findings.length) {
  console.error(findings.join('\n'))
  process.exit(1)
}

let commit = 'working tree'
try { commit = execFileSync('git', ['rev-parse', '--short', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim() } catch {}
console.log(`security-review: ok (${commit}, ${files.length} source files scanned)`)
