import { useEffect, useRef, useState } from 'react'

// Each creation mode keeps its own draft per account (and one for guests),
// so switching modes, theme or language never loses work. Versioned so a
// changed draft shape starts clean instead of loading a broken one.
const VERSION = 1

function storageKey(user: string | undefined, mode: string) {
  return `aigc.draft.v${VERSION}.${user ?? 'guest'}.${mode}`
}

function read<T>(key: string, initial: T): T {
  try {
    const raw = localStorage.getItem(key)
    return raw ? { ...initial, ...(JSON.parse(raw) as Partial<T>) } : initial
  } catch {
    return initial
  }
}

export function useDraft<T extends object>(user: string | undefined, mode: string, initial: T) {
  const key = storageKey(user, mode)
  const [draft, setDraft] = useState<T>(() => read(key, initial))
  const loadedKey = useRef(key)
  // The account can resolve after the first render; load that account's draft then.
  useEffect(() => {
    if (loadedKey.current !== key) {
      loadedKey.current = key
      setDraft(read(key, initial))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  useEffect(() => {
    const timer = setTimeout(() => {
      try {
        localStorage.setItem(key, JSON.stringify(draft))
      } catch {
        // kept in memory for this visit
      }
    }, 300)
    return () => clearTimeout(timer)
  }, [key, draft])
  const update = (patch: Partial<T>) => setDraft((d) => ({ ...d, ...patch }))
  const reset = () => setDraft(initial)
  return [draft, update, reset] as const
}
