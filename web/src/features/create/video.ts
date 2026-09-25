// A single video's reference mode and media. Frames (first/last) and
// reference media are exclusive on the server, so each mode keeps only its
// own media and switching clears the other's.

export type VideoMode = 'text' | 'frames' | 'refs'

export interface VideoMedia {
  first: string[]
  last: string[]
  images: string[]
  videos: string[]
  audios: string[]
}

export interface VideoSettings {
  text: string
  duration: number
  resolution: string
  ratio: string
  enhance: boolean
}

type MediaKey = keyof VideoMedia

const MEDIA_KEYS: MediaKey[] = ['first', 'last', 'images', 'videos', 'audios']

const OWNED: Record<VideoMode, MediaKey[]> = {
  text: [],
  frames: ['first', 'last'],
  refs: ['images', 'videos', 'audios'],
}

/** What switching to `next` would clear: every attached media kind the next mode does not use. */
export function clearedBySwitch(media: VideoMedia, next: VideoMode): { key: MediaKey; count: number }[] {
  const keep = new Set(OWNED[next])
  return MEDIA_KEYS.filter((k) => !keep.has(k) && media[k].length > 0).map((k) => ({ key: k, count: media[k].length }))
}

/** The media left after switching to `next`. */
export function mediaForMode(media: VideoMedia, next: VideoMode): VideoMedia {
  const keep = new Set(OWNED[next])
  return {
    first: keep.has('first') ? media.first : [],
    last: keep.has('last') ? media.last : [],
    images: keep.has('images') ? media.images : [],
    videos: keep.has('videos') ? media.videos : [],
    audios: keep.has('audios') ? media.audios : [],
  }
}

/** Why the request cannot be sent yet, or null when it can. */
export function missingInput(mode: VideoMode, media: VideoMedia, text: string): 'text' | 'firstFrame' | 'reference' | null {
  if (!text.trim()) return 'text'
  if (mode === 'frames' && media.first.length === 0) return 'firstFrame'
  if (mode === 'refs' && media.images.length + media.videos.length + media.audios.length === 0) return 'reference'
  return null
}

/** The spec fields for video.single; the ratio is sent only for text-to-video, where it is required. */
export function videoSpec(mode: VideoMode, media: VideoMedia, s: VideoSettings): Record<string, unknown> {
  return {
    text: s.text,
    duration_seconds: s.duration,
    resolution: s.resolution,
    ...(s.enhance ? { prompt_enhance: true } : {}),
    ...(mode === 'text' ? { ratio: s.ratio } : {}),
    ...(mode === 'frames' && media.first[0] ? { first_frame_asset_id: media.first[0] } : {}),
    ...(mode === 'frames' && media.last[0] ? { last_frame_asset_id: media.last[0] } : {}),
    ...(mode === 'refs' && media.images.length ? { reference_image_asset_ids: media.images } : {}),
    ...(mode === 'refs' && media.videos.length ? { reference_video_asset_ids: media.videos } : {}),
    ...(mode === 'refs' && media.audios.length ? { reference_audio_asset_ids: media.audios } : {}),
  }
}

/** The mode a stored spec was made in. */
export function modeOfSpec(spec: { first_frame_asset_id?: string; last_frame_asset_id?: string; reference_image_asset_ids?: string[]; reference_video_asset_ids?: string[]; reference_audio_asset_ids?: string[] }): VideoMode {
  if (spec.first_frame_asset_id || spec.last_frame_asset_id) return 'frames'
  if (spec.reference_image_asset_ids?.length || spec.reference_video_asset_ids?.length || spec.reference_audio_asset_ids?.length) return 'refs'
  return 'text'
}
