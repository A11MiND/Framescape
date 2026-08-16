import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import AppShell from '../components/AppShell'
import { useAuthStore } from '../lib/authStore'
import { setStoredLang, type Lang } from '../i18n'
import { getStoredTheme, setStoredTheme, type Theme } from '../lib/theme'

// §07/09's "/settings 頁仍缺，目前只有登出按鈕" gap — Rail's own logout
// button still works exactly as before (this isn't a replacement for it,
// just a second, discoverable place for account-level settings to live).
// Language/theme toggles already existed elsewhere (Rail's own EN/中
// button, this page's theme control below) — this page is where they and
// any future account setting belong, instead of scattering them across
// the nav chrome indefinitely.
export default function Settings() {
  const { t, i18n } = useTranslation()
  const logout = useAuthStore((s) => s.logout)
  const me = useQuery({ queryKey: ['me'], queryFn: api.me })
  const [theme, setTheme] = useState<Theme>(getStoredTheme())
  const lang = (i18n.language === 'en' ? 'en' : 'zh') as Lang

  function changeTheme(next: Theme) {
    setTheme(next)
    setStoredTheme(next)
  }

  return (
    <AppShell>
      <div className="mx-auto max-w-2xl space-y-6 px-6 py-8">
        <h1 className="text-lg font-medium">{t('settings.title')}</h1>

        <section className="rounded-xl border border-zinc-800 bg-zinc-900/60 p-5">
          <p className="mb-3 text-xs uppercase tracking-wide text-zinc-500">{t('settings.account')}</p>
          <p className="text-sm text-zinc-300">{me.data?.email}</p>
          <button
            onClick={logout}
            className="mt-4 rounded-lg border border-zinc-700 px-4 py-2 text-sm text-zinc-300 transition hover:border-red-500 hover:text-red-400"
          >
            {t('rail.logout')}
          </button>
        </section>

        <section className="rounded-xl border border-zinc-800 bg-zinc-900/60 p-5">
          <p className="mb-3 text-xs uppercase tracking-wide text-zinc-500">{t('settings.appearance')}</p>
          <div className="flex gap-2">
            {(['dark', 'light'] as Theme[]).map((th) => (
              <button
                key={th}
                onClick={() => changeTheme(th)}
                className={`rounded-lg border px-4 py-2 text-sm transition ${
                  theme === th ? 'border-violet-500 bg-violet-500/10 text-violet-300' : 'border-zinc-800 text-zinc-400 hover:border-zinc-700'
                }`}
              >
                {th === 'dark' ? t('settings.themeDark') : t('settings.themeLight')}
              </button>
            ))}
          </div>
        </section>

        <section className="rounded-xl border border-zinc-800 bg-zinc-900/60 p-5">
          <p className="mb-3 text-xs uppercase tracking-wide text-zinc-500">{t('settings.language')}</p>
          <div className="flex gap-2">
            {(['zh', 'en'] as Lang[]).map((l) => (
              <button
                key={l}
                onClick={() => setStoredLang(l)}
                className={`rounded-lg border px-4 py-2 text-sm transition ${
                  lang === l ? 'border-violet-500 bg-violet-500/10 text-violet-300' : 'border-zinc-800 text-zinc-400 hover:border-zinc-700'
                }`}
              >
                {l === 'zh' ? '中文' : 'English'}
              </button>
            ))}
          </div>
        </section>
      </div>
    </AppShell>
  )
}
