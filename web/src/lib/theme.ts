import { useSyncExternalStore } from 'react'

// The theme preference is per device. A first visit is light (spec D04);
// "system" follows the OS setting live. <html data-theme> always carries
// the resolved theme so both the semantic tokens and the legacy zinc
// inversion key off one attribute.
const STORAGE_KEY = 'aigc.theme'

export type ThemePreference = 'light' | 'dark' | 'system'
export type Theme = 'light' | 'dark'

const listeners = new Set<() => void>()
const media = typeof window !== 'undefined' && window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null

function readPreference(): ThemePreference {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v === 'dark' || v === 'system') return v
  } catch {
    // storage unavailable: fall back to the default
  }
  return 'light'
}

let preference: ThemePreference = readPreference()

export function resolveTheme(pref: ThemePreference, systemDark: boolean): Theme {
  if (pref === 'system') return systemDark ? 'dark' : 'light'
  return pref
}

function apply() {
  document.documentElement.dataset.theme = resolveTheme(preference, media?.matches ?? false)
  listeners.forEach((l) => l())
}

export function getThemePreference(): ThemePreference {
  return preference
}

export function setThemePreference(next: ThemePreference) {
  preference = next
  try {
    localStorage.setItem(STORAGE_KEY, next)
  } catch {
    // the choice still applies for this visit
  }
  apply()
}

export function getResolvedTheme(): Theme {
  return resolveTheme(preference, media?.matches ?? false)
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

/** The current preference, re-rendering when it or the OS setting changes. */
export function useThemePreference(): [ThemePreference, Theme] {
  const pref = useSyncExternalStore(subscribe, getThemePreference)
  const resolved = useSyncExternalStore(subscribe, getResolvedTheme)
  return [pref, resolved]
}

// Legacy names used by pages not yet rebuilt.
export const getStoredTheme = getResolvedTheme
export const setStoredTheme = (theme: Theme) => setThemePreference(theme)

media?.addEventListener('change', () => {
  if (preference === 'system') apply()
})

// Applied at module load (imported from main.tsx) so the first paint uses
// the right theme.
apply()
