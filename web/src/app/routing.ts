export const CREATE_MODES = ['image', 'image-sequence', 'gpt', 'video', 'video-sequence', 'comic', 'comic-classic'] as const
export type CreateMode = (typeof CREATE_MODES)[number]

const LAST_MODE_KEY = 'aigc.create.mode'

export function isCreateMode(s: string | undefined): s is CreateMode {
  return (CREATE_MODES as readonly string[]).includes(s ?? '')
}

export function lastCreateMode(): CreateMode {
  try {
    const v = localStorage.getItem(LAST_MODE_KEY) ?? undefined
    return isCreateMode(v) ? v : 'image'
  } catch {
    return 'image'
  }
}

export function rememberCreateMode(mode: CreateMode) {
  try {
    localStorage.setItem(LAST_MODE_KEY, mode)
  } catch {
    // not remembered for the next visit
  }
}

interface PrefillJob {
  workflowName: string
  spec: { comic_mode?: string; image_provider?: string }
}

/**
 * Where a "create again" prefill opens: the mode of the job's workflow, not
 * the mode used last. The comic editor reads its own prefill shape.
 */
export function prefillTarget(state: unknown): { mode: CreateMode; state: unknown } | null {
  const suggestion = (state as { prefillSuggestion?: { kind?: string } } | null)?.prefillSuggestion
  if (suggestion) return { mode: suggestion.kind === 'to-sequence' ? 'video-sequence' : 'video', state }
  const job = (state as { prefillJob?: PrefillJob } | null)?.prefillJob
  if (!job) return null
  const { workflowName, spec } = job
  if (workflowName === 'image.comic4') {
    return spec?.comic_mode ? { mode: 'comic', state: { prefillComic: spec } } : { mode: 'comic-classic', state }
  }
  if (workflowName === 'image.sequence') return { mode: 'image-sequence', state }
  if (workflowName === 'video.single') return { mode: 'video', state }
  if (workflowName === 'video.sequence') return { mode: 'video-sequence', state }
  if (spec?.image_provider === 'openai') return { mode: 'gpt', state }
  return { mode: 'image', state }
}

/** Only same-site paths are followed after sign-in. */
export function safeReturnPath(v: unknown): string | null {
  return typeof v === 'string' && v.startsWith('/') && !v.startsWith('//') && !v.startsWith('/login') ? v : null
}
