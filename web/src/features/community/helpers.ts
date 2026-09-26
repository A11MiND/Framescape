import { useQuery } from '@tanstack/react-query'
import { communityApi, type CommunityWork } from '../../lib/api/community'

/** A work's display title: the first line of its public prompt. */
export function workTitle(t: (k: string) => string, w: CommunityWork) {
  const line = (w.prompt ?? '').trim().split('\n')[0]
  return line ? line.slice(0, 40) : t('untitled')
}

export function useStreak(enabled: boolean) {
  return useQuery({ queryKey: ['community', 'streak'], queryFn: communityApi.streak, enabled })
}
