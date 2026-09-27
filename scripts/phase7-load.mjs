#!/usr/bin/env node
// Dependency-free local read-path smoke load. Point LOAD_URL at a local API
// route (the default is the health endpoint), never at a production URL.
const url = process.env.LOAD_URL ?? 'http://127.0.0.1:8080/internal/health'
const total = Number(process.env.LOAD_REQUESTS ?? 300)
const concurrency = Math.max(1, Number(process.env.LOAD_CONCURRENCY ?? 32))
const expectedP95 = Number(process.env.LOAD_EXPECT_P95_MS ?? 200)
if (!/^https?:\/\/(127\.0\.0\.1|localhost)(:\d+)?\//.test(url)) {
  throw new Error(`refusing non-local LOAD_URL: ${url}`)
}

const durations = []
let next = 0
let failures = 0
const started = performance.now()
async function worker() {
  while (true) {
    const index = next++
    if (index >= total) return
    const t = performance.now()
    try {
      const response = await fetch(url, { headers: { Accept: 'application/json' } })
      if (!response.ok) failures++
      await response.arrayBuffer()
    } catch {
      failures++
    } finally {
      durations.push(performance.now() - t)
    }
  }
}
await Promise.all(Array.from({ length: Math.min(concurrency, total) }, worker))
durations.sort((a, b) => a - b)
const percentile = (p) => durations[Math.min(durations.length - 1, Math.ceil(durations.length * p) - 1)] ?? 0
const elapsed = performance.now() - started
const report = {
  url,
  requests: total,
  concurrency: Math.min(concurrency, total),
  failures,
  elapsed_ms: Math.round(elapsed),
  rps: Number((total / (elapsed / 1000)).toFixed(1)),
  p50_ms: Number(percentile(0.5).toFixed(1)),
  p95_ms: Number(percentile(0.95).toFixed(1)),
}
console.log(JSON.stringify(report, null, 2))
if (failures || report.p95_ms > expectedP95) process.exit(1)
