import { useRef } from 'react'

/**
 * Remembers what had focus when an overlay opened and returns focus there
 * on close. Radix only does this when opened by its own Trigger, while most
 * overlays here open from state (row menus, confirmations). The element is
 * read during the render that opens the overlay, before any effect can move
 * focus into it.
 */
export function useReturnFocus(open: boolean) {
  const previous = useRef<HTMLElement | null>(null)
  const wasOpen = useRef(false)
  if (open && !wasOpen.current && typeof document !== 'undefined' && document.activeElement instanceof HTMLElement) {
    previous.current = document.activeElement
  }
  wasOpen.current = open
  return (event: Event) => {
    const el = previous.current
    previous.current = null
    if (el && el.isConnected) {
      event.preventDefault()
      el.focus()
    }
  }
}
