import { useSyncExternalStore } from 'react'

/** Whether a CSS media query currently matches, kept in sync with the viewport. */
export function useMediaQuery(query: string) {
  return useSyncExternalStore(
    (notify) => {
      const m = window.matchMedia(query)
      m.addEventListener('change', notify)
      return () => m.removeEventListener('change', notify)
    },
    () => window.matchMedia(query).matches,
    () => false,
  )
}
