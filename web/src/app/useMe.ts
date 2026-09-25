import { useQuery } from '@tanstack/react-query'
import { useAuthStore } from '../lib/authStore'
import { accountApi } from '../lib/api/account'
import { keys } from '../lib/api/keys'

/** The signed-in account; disabled for guests. */
export function useMe() {
  const signedIn = useAuthStore((s) => Boolean(s.accessToken))
  return useQuery({ queryKey: keys.me, queryFn: accountApi.me, enabled: signedIn })
}
