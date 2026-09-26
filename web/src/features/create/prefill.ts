// Seeds a creation draft from navigation state: "create again" from a task
// (prefillJob) or "use this character" from the character library.

const SLOT_LETTERS = 'ABCDEF'

/** Character ids as the request's named slots. */
export function characterSlots(ids: string[]) {
  return ids.slice(0, SLOT_LETTERS.length).map((id, i) => ({ slot: SLOT_LETTERS[i], character_id: id }))
}

export interface PrefillFields {
  text?: string
  n?: number
  reference?: string[]
  references?: string[]
  characters?: string[]
  presets?: string[]
  size?: string
  quality?: string
  mode?: 'general' | 'direct'
  panels?: string[]
  story?: string
  shots?: string[]
  /** Per shot, the 1-based number of an earlier shot it builds on, or 0. */
  shotRefs?: number[]
  sequenceMode?: 'quick' | 'continuity'
  provider?: 'openai' | 'minimax'
  video?: {
    first_frame_asset_id?: string
    last_frame_asset_id?: string
    reference_image_asset_ids?: string[]
    reference_video_asset_ids?: string[]
    reference_audio_asset_ids?: string[]
    duration_seconds?: number
    resolution?: string
    ratio?: string
    prompt_enhance?: boolean
  }
}

interface PrefillSpec {
  text?: string
  n?: number
  source_image_asset_id?: string
  reference_image_asset_ids?: string[]
  characters?: { character_id: string }[]
  preset_ids?: string[]
  image_size?: string
  image_quality?: string
  comic_mode?: string
  panels?: string[]
  story?: string
  shots?: string[]
  shot_source_refs?: number[]
  image_sequence_mode?: string
  image_provider?: string
  first_frame_asset_id?: string
  last_frame_asset_id?: string
  reference_video_asset_ids?: string[]
  reference_audio_asset_ids?: string[]
  duration_seconds?: number
  resolution?: string
  ratio?: string
  prompt_enhance?: boolean
}

export function readPrefill(state: unknown, workflow: string): PrefillFields | null {
  const s = (state ?? {}) as { prefillJob?: { workflowName?: string; spec?: PrefillSpec }; prefillCharacterId?: string; prefillReferenceId?: string }
  if (s.prefillCharacterId) return { characters: [s.prefillCharacterId] }
  if (s.prefillReferenceId) return { reference: [s.prefillReferenceId], references: [s.prefillReferenceId] }
  const job = s.prefillJob
  if (!job?.spec) return null
  const spec = job.spec
  const out: PrefillFields = {
    text: spec.text ?? '',
    n: spec.n ?? 1,
    characters: (spec.characters ?? []).map((c) => c.character_id),
    presets: spec.preset_ids ?? [],
  }
  if (job.workflowName === 'image.comic4' && spec.comic_mode === 'direct') {
    return { mode: 'direct', text: spec.text ?? '', references: spec.reference_image_asset_ids ?? [] }
  }
  if (job.workflowName !== workflow) return null
  if (spec.source_image_asset_id) out.reference = [spec.source_image_asset_id]
  if (spec.reference_image_asset_ids) out.references = spec.reference_image_asset_ids
  if (spec.image_size) out.size = spec.image_size
  if (spec.image_quality) out.quality = spec.image_quality
  if (spec.panels) out.panels = spec.panels
  if (spec.story) out.story = spec.story
  if (spec.shots) out.shots = spec.shots
  if (spec.shot_source_refs) out.shotRefs = spec.shot_source_refs
  if (spec.image_sequence_mode) out.sequenceMode = spec.image_sequence_mode === 'continuity' ? 'continuity' : 'quick'
  if (workflow === 'video.single') out.video = spec
  if (spec.image_provider) out.provider = spec.image_provider === 'openai' ? 'openai' : 'minimax'
  return out
}
