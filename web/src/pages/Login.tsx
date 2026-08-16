import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api, ApiError } from '../lib/api'
import { useAuthStore } from '../lib/authStore'

// F1.1: email register/login. Minimal single-form W1 version — no
// separate register/login screens yet, just a mode toggle. F1.2's anonymous
// trial used to live here as its own card — it's moved to `/` (Studio's
// guest state) now that `/` is reachable without auth, so trying it no
// longer requires finding this page first.
export default function Login() {
  const { t } = useTranslation()
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const setTokens = useAuthStore((s) => s.setTokens)
  const navigate = useNavigate()

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const tokens = mode === 'login' ? await api.login(email, password) : await api.register(email, password)
      setTokens(tokens.access_token, tokens.refresh_token)
      navigate('/')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t('common.somethingWentWrong'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden bg-zinc-950 p-6">
      {/* Ambient glow — the app has no logo asset, so a soft brand-colored
          wash behind the card is the lightest-touch way to keep this page
          from reading as a bare, un-designed void. */}
      <div className="pointer-events-none absolute inset-0 overflow-hidden">
        <div className="absolute left-1/2 top-[28%] h-[520px] w-[520px] -translate-x-1/2 -translate-y-1/2 rounded-full bg-violet-600/20 blur-[140px]" />
      </div>

      <div className="relative w-full max-w-sm space-y-5">
        <div className="text-center">
          <div className="mx-auto mb-3 flex h-12 w-12 items-center justify-center rounded-2xl bg-gradient-to-br from-violet-400 to-violet-600 text-xl text-white shadow-lg shadow-violet-950/40">
            ✦
          </div>
          <h1 className="text-xl font-semibold text-zinc-50">{t('brand.name')}</h1>
          <p className="mt-1 text-sm text-zinc-500">{t('brand.tagline')}</p>
        </div>

        <form onSubmit={submit} className="rounded-2xl border border-zinc-800 bg-zinc-900/90 p-6 shadow-2xl shadow-black/40">
          <div className="mb-5 flex rounded-lg bg-zinc-950 p-1">
            {(['login', 'register'] as const).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => setMode(m)}
                className={`flex-1 rounded-md py-1.5 text-sm font-medium transition ${
                  mode === m ? 'bg-violet-500 text-white shadow' : 'text-zinc-500 hover:text-zinc-300'
                }`}
              >
                {m === 'login' ? t('login.signIn') : t('login.signUp')}
              </button>
            ))}
          </div>

          <div className="space-y-3">
            <input
              type="email"
              required
              placeholder={t('login.email')}
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2.5 text-zinc-50 outline-none transition focus:border-violet-500"
            />
            <input
              type="password"
              required
              minLength={8}
              placeholder={t('login.passwordPlaceholder')}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2.5 text-zinc-50 outline-none transition focus:border-violet-500"
            />
          </div>

          {error && <p className="mt-3 text-sm text-red-400">{error}</p>}

          <button
            type="submit"
            disabled={busy}
            className="mt-4 w-full rounded-lg bg-violet-500 px-3 py-2.5 font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
          >
            {busy ? t('common.processing') : mode === 'login' ? t('login.signIn') : t('login.createAccount')}
          </button>
        </form>

        <p className="text-center text-xs text-zinc-600">
          {t('login.notSure')}
          <Link to="/" className="text-violet-400 hover:text-violet-300">
            {t('login.tryFree')}
          </Link>
        </p>
      </div>
    </div>
  )
}
