import type { AssetInfo } from '../../lib/api/assets'

/** A library item's display name: the first line of its prompt, or a generic one by kind. */
export function assetTitle(t: (k: string) => string, a: AssetInfo) {
  const prompt = (a.prompt ?? '').trim()
  return prompt ? prompt.split('\n')[0].slice(0, 60) : t(`untitled.${a.type === 'video' ? 'video' : a.type === 'audio' ? 'audio' : 'image'}`)
}
