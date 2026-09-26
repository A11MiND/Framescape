import { useQuery } from '@tanstack/react-query'
import { createApi } from '../../lib/api/create'
import { keys } from '../../lib/api/keys'

/** Character limits from capabilities, the same values the server enforces. */
export function useCharacterLimits() {
  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities, staleTime: 60_000 })
  const c = caps.data?.characters
  return { maxRefs: c?.max_references ?? 3, nameMax: c?.name_max_chars ?? 64, descriptionMax: c?.description_max_chars ?? 1024 }
}
