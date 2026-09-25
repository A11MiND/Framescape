import { describe, expect, it } from 'vitest'
import { clearedBySwitch, mediaForMode, missingInput, modeOfSpec, videoSpec, type VideoMedia } from './video'

const none: VideoMedia = { first: [], last: [], images: [], videos: [], audios: [] }
const settings = { text: 'a tram', duration: 6, resolution: '768P', ratio: '16:9', enhance: false }

describe('single video', () => {
  it('asks before clearing only when something would be cleared', () => {
    expect(clearedBySwitch(none, 'frames')).toEqual([])
    expect(clearedBySwitch({ ...none, first: ['a'] }, 'frames')).toEqual([])
    expect(clearedBySwitch({ ...none, first: ['a'], last: ['b'] }, 'refs')).toEqual([
      { key: 'first', count: 1 },
      { key: 'last', count: 1 },
    ])
    expect(clearedBySwitch({ ...none, images: ['a', 'b'], audios: ['c'] }, 'text')).toEqual([
      { key: 'images', count: 2 },
      { key: 'audios', count: 1 },
    ])
  })

  it('looks only at media, not at other draft fields', () => {
    const draft = { ...none, mode: 'text', text: 'a tram', ratio: '16:9', characters: ['c'] }
    expect(clearedBySwitch(draft, 'frames')).toEqual([])
  })

  it('keeps only the media the next mode uses', () => {
    expect(mediaForMode({ first: ['a'], last: [], images: ['b'], videos: [], audios: [] }, 'refs')).toEqual({ ...none, images: ['b'] })
  })

  it('needs a description, a first frame in frames mode and a reference in references mode', () => {
    expect(missingInput('text', none, ' ')).toBe('text')
    expect(missingInput('frames', { ...none, last: ['b'] }, 'x')).toBe('firstFrame')
    expect(missingInput('refs', none, 'x')).toBe('reference')
    expect(missingInput('refs', { ...none, audios: ['a'] }, 'x')).toBeNull()
  })

  it('sends the ratio only for text-to-video and never mixes frames with references', () => {
    expect(videoSpec('text', none, settings)).toEqual({ text: 'a tram', duration_seconds: 6, resolution: '768P', ratio: '16:9' })
    const frames = videoSpec('frames', { ...none, first: ['f'], images: ['stale'] }, { ...settings, enhance: true })
    expect(frames).toEqual({ text: 'a tram', duration_seconds: 6, resolution: '768P', prompt_enhance: true, first_frame_asset_id: 'f' })
    expect(videoSpec('refs', { ...none, videos: ['v'], first: ['stale'] }, settings)).toEqual({
      text: 'a tram',
      duration_seconds: 6,
      resolution: '768P',
      reference_video_asset_ids: ['v'],
    })
  })

  it('recognizes the mode of a stored spec', () => {
    expect(modeOfSpec({ last_frame_asset_id: 'x' })).toBe('frames')
    expect(modeOfSpec({ reference_audio_asset_ids: ['a'] })).toBe('refs')
    expect(modeOfSpec({})).toBe('text')
  })
})
