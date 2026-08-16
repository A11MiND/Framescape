import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, ApiError } from '../lib/api'
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

  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [pwSuccess, setPwSuccess] = useState(false)
  const changePassword = useMutation({
    mutationFn: () => api.changePassword(currentPassword, newPassword),
    onSuccess: () => {
      setCurrentPassword('')
      setNewPassword('')
      setPwSuccess(true)
    },
    onMutate: () => setPwSuccess(false),
  })

  function submitPasswordChange(e: React.FormEvent) {
    e.preventDefault()
    changePassword.mutate()
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
          <p className="mb-3 text-xs uppercase tracking-wide text-zinc-500">{t('settings.security')}</p>
          <form onSubmit={submitPasswordChange} className="space-y-3">
            <input
              type="password"
              required
              placeholder={t('settings.currentPasswordPlaceholder')}
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
            />
            <input
              type="password"
              required
              minLength={8}
              placeholder={t('settings.newPasswordPlaceholder')}
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
            />
            {changePassword.isError && (
              <p className="text-sm text-red-400">
                {changePassword.error instanceof ApiError ? changePassword.error.message : t('common.somethingWentWrong')}
              </p>
            )}
            {pwSuccess && <p className="text-sm text-emerald-400">{t('settings.passwordChanged')}</p>}
            <button
              type="submit"
              disabled={changePassword.isPending}
              className="rounded-lg border border-zinc-700 px-4 py-2 text-sm text-zinc-200 transition hover:border-violet-500 hover:text-violet-300 disabled:opacity-50"
            >
              {changePassword.isPending ? t('common.saving') : t('settings.changePassword')}
            </button>
          </form>
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
