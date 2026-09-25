import { useCallback, useSyncExternalStore } from 'react'

// The project chosen in the top bar, per account and device. New work is
// filed under it by default; null means all projects.
const listeners = new Set<() => void>()
const storageKey = (userId: string) => `aigc.project.${userId}`

function read(userId: string | undefined): string | null {
  if (!userId) return null
  try {
    return localStorage.getItem(storageKey(userId))
  } catch {
    return null
  }
}

export function setCurrentProject(userId: string, projectId: string | null) {
  try {
    if (projectId) localStorage.setItem(storageKey(userId), projectId)
    else localStorage.removeItem(storageKey(userId))
  } catch {
    // kept for this visit only through the listeners below
  }
  listeners.forEach((l) => l())
}

export function useCurrentProject(userId: string | undefined): [string | null, (id: string | null) => void] {
  const subscribe = useCallback((l: () => void) => {
    listeners.add(l)
    return () => {
      listeners.delete(l)
    }
  }, [])
  const value = useSyncExternalStore(subscribe, () => read(userId))
  const set = useCallback((id: string | null) => userId && setCurrentProject(userId, id), [userId])
  return [value, set]
}
