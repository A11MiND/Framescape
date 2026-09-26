import { useEffect, useRef } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { MAX_CHARACTERS } from './CharacterPicker'

/** Adds a character to those already bound, dropping the oldest past the cap. */
export function withCharacter(bound: string[], id: string) {
  return [...bound.filter((x) => x !== id), id].slice(-MAX_CHARACTERS)
}

/**
 * "Use in creation" from the character library binds that character and
 * keeps the rest of the draft as it is.
 */
export function useCharacterPrefill(bound: string[], set: (ids: string[]) => void) {
  const location = useLocation()
  const navigate = useNavigate()
  // Effects can run twice for one navigation; each is applied once.
  const handled = useRef<unknown>(null)
  useEffect(() => {
    if (handled.current === location.state) return
    const id = (location.state as { prefillCharacterId?: string } | null)?.prefillCharacterId
    if (!id) return
    handled.current = location.state
    set(withCharacter(bound, id))
    navigate('.', { replace: true, state: {} })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state])
}
