// §07/09's "淺色主題" gap. Same hand-rolled localStorage pattern as
// i18n/index.ts's Lang (no need for a whole plugin for one string), applying
// the choice as a `data-theme` attribute on <html> — index.css's
// `[data-theme="light"]` block is what actually re-themes the app by
// overriding the Tailwind color variables every component already renders
// through.
const STORAGE_KEY = 'aigc.theme'

export type Theme = 'dark' | 'light'

export function getStoredTheme(): Theme {
  return localStorage.getItem(STORAGE_KEY) === 'light' ? 'light' : 'dark'
}

export function applyTheme(theme: Theme) {
  if (theme === 'light') {
    document.documentElement.dataset.theme = 'light'
  } else {
    delete document.documentElement.dataset.theme
  }
}

export function setStoredTheme(theme: Theme) {
  localStorage.setItem(STORAGE_KEY, theme)
  applyTheme(theme)
}

// Applied once at module load (imported from main.tsx) so the correct
// theme is set before first paint — same reasoning as i18n's `lng:
// getStoredLang()` running at init instead of in a post-mount effect,
// avoiding a flash of the wrong theme.
applyTheme(getStoredTheme())
