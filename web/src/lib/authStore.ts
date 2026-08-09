import { create } from 'zustand'

interface AuthState {
  accessToken: string | null
  refreshToken: string | null
  setTokens: (access: string, refresh: string) => void
  logout: () => void
}

const STORAGE_KEY = 'aigc.auth'

function loadInitial(): { accessToken: string | null; refreshToken: string | null } {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { accessToken: null, refreshToken: null }
    return JSON.parse(raw)
  } catch {
    return { accessToken: null, refreshToken: null }
  }
}

export const useAuthStore = create<AuthState>((set) => ({
  ...loadInitial(),
  setTokens: (accessToken, refreshToken) => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ accessToken, refreshToken }))
    set({ accessToken, refreshToken })
  },
  logout: () => {
    localStorage.removeItem(STORAGE_KEY)
    set({ accessToken: null, refreshToken: null })
  },
}))
