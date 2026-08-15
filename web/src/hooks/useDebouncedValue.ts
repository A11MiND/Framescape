import { useEffect, useState } from 'react'

// §19.4.1's "参数变更触发防抖 300ms 后调用 POST /jobs/estimate" — generic
// enough that Studio's estimate call is its only user today but nothing
// about it is estimate-specific.
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(timer)
  }, [value, delayMs])
  return debounced
}
