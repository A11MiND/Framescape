// §19.5.3's content-moderation row: "阻断式：明确说明「描述涉及敏感内容，未
// 扣除积分」，不展示具体触发词". Node failures whose message starts with
// `sensitive_content:` come straight from the real provider's moderation
// response (minimax.image/minimax.video's own convention, W3/W5) and may
// echo back the specific flagged phrase — never shown to the user verbatim.
const MODERATION_PREFIX = 'sensitive_content:'

// Aether's own literal OnTaskTimeout message (engine.go) — fires when a
// task's configured `timeout` is exceeded, after any configured retries
// are already exhausted. Shown verbatim to a real user otherwise (found
// live off a failed image.comic4 panel), with no indication retries had
// already happened silently in the background.
const WATCHDOG_TIMEOUT_MESSAGE = 'task deadline exceeded (watchdog)'

// minimax.image/minimax.video's own convention (image.go/video.go) for a
// hard, synchronous rejection from MiniMax's own API — the platform's own
// MiniMax account balance is insufficient, so MiniMax never even started
// generating; this is not the end user's in-app balance (studio.
// insufficientBalance already covers that, checked client-side before
// submit) and not a timeout with something to recover — there is nothing on
// MiniMax's side to retrieve. Shown verbatim (raw provider JSON) otherwise,
// which read as "the API took my money and gave me nothing" — found live
// off a real failed video.sequence shot.
const PROVIDER_INSUFFICIENT_BALANCE_PREFIX = 'insufficient_balance:'

// Aether's own generic aggregator messages (engine_sched.go) — every
// container-level node (the top-level "main" DAG, and any Loop container
// like image.comic4's "panels" or image.sequence's "shots") carries one of
// these instead of the real provider error. displayNodeError leaves it
// as-is (it's not sensitive, just unhelpful); firstSpecificError below is
// what actually skips past it to find the real cause. All three of
// aether's own phase outcomes have their own wording ("errored" is the
// generic-failure phase, "failed" a distinct one, "were cancelled" for
// PhaseCancelled) — matching only one of the three left the other two
// (most visibly "one or more tasks errored") displayed to users verbatim,
// found live off a real failed image.comic4 panel.
const GENERIC_AGGREGATE_ERRORS = new Set([
  'one or more tasks errored',
  'one or more tasks failed',
  'one or more tasks were cancelled',
])

// t is threaded through explicitly rather than imported directly — see
// jobGraph.ts's buildJobGraph doc for why (same pattern: a pure function
// called from render, not a hook).
export function displayNodeError(raw: string | undefined | null, t: (key: string) => string): string {
  if (!raw) return t('errors.unknown')
  if (raw.startsWith(MODERATION_PREFIX)) return t('errors.moderationBlocked')
  if (raw === WATCHDOG_TIMEOUT_MESSAGE) return t('errors.watchdogTimeout')
  if (raw.startsWith(PROVIDER_INSUFFICIENT_BALANCE_PREFIX)) return t('errors.providerInsufficientBalance')
  return raw
}

// Picks the first node whose error says something more specific than the
// generic aggregator message — the container node that's easiest to look up
// (job.nodes.find by a known name) is almost never the one with the useful
// error, since failures happen in the leaf task underneath it.
export function firstSpecificError(nodes: { name: string; error: string }[]): string | undefined {
  return nodes.find((n) => n.error && !GENERIC_AGGREGATE_ERRORS.has(n.error))?.error
}
