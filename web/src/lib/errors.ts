// §19.5.3's content-moderation row: "阻断式：明确说明「描述涉及敏感内容，未
// 扣除积分」，不展示具体触发词". Node failures whose message starts with
// `sensitive_content:` come straight from the real provider's moderation
// response (minimax.image/minimax.video's own convention, W3/W5) and may
// echo back the specific flagged phrase — never shown to the user verbatim.
const MODERATION_PREFIX = 'sensitive_content:'

// Aether's own generic aggregator message — every container-level node (the
// top-level "main" DAG, and any Loop container like image.comic4's "panels"
// or image.sequence's "shots") carries this instead of the real provider
// error, confirmed against real failed runs. displayNodeError leaves it
// as-is (it's not sensitive, just unhelpful); firstSpecificError below is
// what actually skips past it to find the real cause.
const GENERIC_AGGREGATE_ERROR = 'one or more tasks failed'

export function displayNodeError(raw: string | undefined | null): string {
  if (!raw) return '未知错误'
  if (raw.startsWith(MODERATION_PREFIX)) return '描述涉及敏感内容，未扣除积分'
  return raw
}

// Picks the first node whose error says something more specific than the
// generic aggregator message — the container node that's easiest to look up
// (job.nodes.find by a known name) is almost never the one with the useful
// error, since failures happen in the leaf task underneath it.
export function firstSpecificError(nodes: { name: string; error: string }[]): string | undefined {
  return nodes.find((n) => n.error && n.error !== GENERIC_AGGREGATE_ERROR)?.error
}
