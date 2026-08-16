import { useEffect, type RefObject } from 'react'

// Four popovers this session (PresetCarousel, CharacterSlotPicker,
// MentionTextarea, NotificationCenter) each opened on a trigger click and
// only ever closed again via selecting an item — clicking anywhere else on
// the page left them stuck open. `mousedown` (not `click`) is deliberate:
// it fires before the trigger button's own onClick toggles `open`, so a
// click on the trigger itself never fights with this listener (the trigger
// lives inside `ref`'s subtree, so `contains` is true for it and onOutside
// is correctly skipped) — using `click` here would instead race the two
// handlers and could immediately reopen what the trigger just closed.
// `enabled` lets callers only pay for the listener while actually open.
export function useClickOutside(ref: RefObject<HTMLElement | null>, onOutside: () => void, enabled: boolean) {
  useEffect(() => {
    if (!enabled) return
    function handlePointerDown(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onOutside()
      }
    }
    document.addEventListener('mousedown', handlePointerDown)
    return () => document.removeEventListener('mousedown', handlePointerDown)
  }, [ref, onOutside, enabled])
}
