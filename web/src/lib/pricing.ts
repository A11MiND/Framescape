// Mirrors internal/application/creditsvc's pricing (§12.1/§12.2) exactly.
// No backend /jobs/estimate endpoint exists yet (PRD §13.3 describes one,
// not built) — these are pure, static-priced formulas, safe to duplicate
// client-side for a live estimate rather than round-tripping the server on
// every keystroke. If the pricing table or margin K ever becomes dynamic
// (e.g. per-user discounts), this needs to move server-side for real.

const MARGIN_K = 2.0
const IMAGE_RATE_YUAN = 0.025
const VIDEO_RATE_YUAN: Record<string, number> = { '768P': 0.5, '2K': 0.8 }

export function creditsFromYuan(costYuan: number): number {
  if (costYuan <= 0) return 0
  return Math.max(1, Math.ceil((costYuan * MARGIN_K) / 0.1))
}

export function estimateImageCredits(n: number): number {
  return creditsFromYuan(Math.max(1, n) * IMAGE_RATE_YUAN)
}

// Mirrors creditsvc.EstimatePerNodeImageCredits — image.comic4/
// image.sequence each bill one MiniMax call per panel/shot independently,
// so n separate §12.2 "minimum 1 credit" floors is the correct estimate,
// not one combined-cost floor for n images the way estimateImageCredits
// above works (that one's image.single's own shape: one call producing n
// outputs). Using estimateImageCredits for comic4/sequence here used to
// under-price them the same way the server-side bug this mirrors had
// (4 panels floors to 4 credits total, not 2).
export function estimatePerNodeImageCredits(n: number): number {
  return Math.max(1, n) * creditsFromYuan(IMAGE_RATE_YUAN)
}

export function estimateVideoCredits(durationSeconds: number, resolution: string): number {
  const rate = VIDEO_RATE_YUAN[resolution] ?? VIDEO_RATE_YUAN['768P']
  return creditsFromYuan(durationSeconds * rate)
}

// F6.10's H3-Context-IR node bills per token (§3.4: ¥5.80/M input, ¥23.00/M
// output), not duration — mirrors creditsvc.EstimatePromptEnhanceCredits'
// same conservative ~500 in / ~1500 out token assumption for a live estimate.
export function estimatePromptEnhanceCredits(): number {
  return creditsFromYuan((500 / 1_000_000) * 5.8 + (1500 / 1_000_000) * 23.0)
}

// F5.4's story-split node (MiniMax-M3) — same conservative token-count
// assumption as creditsvc.EstimateStorySplitCredits.
export function estimateStorySplitCredits(): number {
  return creditsFromYuan((300 / 1_000_000) * 2.1 + (400 / 1_000_000) * 8.4)
}
