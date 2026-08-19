import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api, ApiError } from '../lib/api'
import { useAuthStore } from '../lib/authStore'
import { getStoredLang, setStoredLang, type Lang } from '../i18n'
import { getStoredTheme, setStoredTheme, type Theme } from '../lib/theme'

// §07's "登录页只有一张孤零零的表单卡片" gap — jimeng-style auth pages pair
// the form with a showcase of what the product actually makes, which this
// app had no equivalent of. SHOWCASE_IMAGES are a small fixed set of
// pre-generated demo images (real minimax.image output, generated once and
// committed as static assets under public/login-showcase/ — NOT generated
// live from this page, since that would let an unauthenticated visitor
// trigger paid provider calls). Purely decorative, so no alt text copy to
// translate; the two-column split only appears at lg: and up, matching this
// app's existing mobile-first pattern of hiding secondary content on
// narrow viewports rather than reflowing it.
const SHOWCASE_IMAGES = [
  'warrior-watercolor',
  'mage-comic',
  'genki-girl',
  'knight-medieval',
  'reading-watercolor',
  'streetwear-comic',
  'archer-medieval',
  'victory-genki',
]

// Google's own multi-color "G" logomark — the one icon in this codebase
// that's deliberately full-color rather than a plain stroke shape (the
// convention everywhere else, see Rail.tsx's own doc on why bare emoji
// glyphs got replaced with SVGs): Google's brand guidelines for a
// "Sign in with Google" button specifically require this mark, not a
// monochrome stand-in.
function GoogleLogo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 20 20" className={className}>
      <path fill="#4285F4" d="M19.6 10.23c0-.68-.06-1.36-.18-2.02H10v3.83h5.38a4.6 4.6 0 0 1-2 3.02v2.5h3.23c1.9-1.75 2.99-4.33 2.99-7.33Z" />
      <path fill="#34A853" d="M10 20c2.7 0 4.96-.89 6.61-2.42l-3.23-2.5c-.9.6-2.05.95-3.38.95-2.6 0-4.8-1.76-5.59-4.12H1.08v2.59A10 10 0 0 0 10 20Z" />
      <path fill="#FBBC05" d="M4.41 11.9a6 6 0 0 1 0-3.8V5.5H1.08a10 10 0 0 0 0 9l3.33-2.6Z" />
      <path fill="#EA4335" d="M10 3.98c1.47 0 2.79.5 3.83 1.5l2.87-2.87C14.95 1 12.7 0 10 0A10 10 0 0 0 1.08 5.5l3.33 2.6C5.2 5.74 7.4 3.98 10 3.98Z" />
    </svg>
  )
}

// F1.1: email register/login. Minimal single-form W1 version — no
// separate register/login screens yet, just a mode toggle. F1.2's anonymous
// trial used to live here as its own card — it's moved to `/` (Studio's
// guest state) now that `/` is reachable without auth, so trying it no
// longer requires finding this page first.
// google is the one bit of `window` this file reaches into directly — the
// GSI client script (loaded lazily, see loadGoogleScript below) attaches
// itself there, and there's no first-party type package worth adding for
// a handful of fields this narrow.
declare global {
  interface Window {
    google?: {
      accounts: {
        id: {
          initialize: (config: { client_id: string; callback: (resp: { credential: string }) => void }) => void
          prompt: () => void
        }
      }
    }
  }
}

let googleScriptPromise: Promise<void> | null = null
function loadGoogleScript(): Promise<void> {
  if (window.google) return Promise.resolve()
  if (!googleScriptPromise) {
    googleScriptPromise = new Promise((resolve, reject) => {
      const s = document.createElement('script')
      s.src = 'https://accounts.google.com/gsi/client'
      s.async = true
      s.onload = () => resolve()
      s.onerror = () => reject(new Error('failed to load Google script'))
      document.head.appendChild(s)
    })
  }
  return googleScriptPromise
}

export default function Login() {
  const { t, i18n } = useTranslation()
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [method, setMethod] = useState<'email' | 'phone'>('email')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [emailCode, setEmailCode] = useState('')
  const [emailCodeSent, setEmailCodeSent] = useState(false)
  const [emailCodeBusy, setEmailCodeBusy] = useState(false)
  const [phone, setPhone] = useState('')
  const [phoneCode, setPhoneCode] = useState('')
  const [codeSent, setCodeSent] = useState(false)
  const [codeBusy, setCodeBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const setTokens = useAuthStore((s) => s.setTokens)
  const navigate = useNavigate()

  // §07's "Google 登录/手机号注册" ask — both real providers require
  // credentials this app was never handed (Google OAuth client ID, an SMS
  // provider's API key), so both paths degrade to a clear, translated
  // "not configured" message instead of a raw backend error string — see
  // auth_oauth.go's own doc for the server side of this gate.
  function friendlyError(err: unknown): string {
    if (err instanceof ApiError) {
      if (err.code === 'google_not_configured') return t('login.googleNotConfigured')
      if (err.code === 'sms_not_configured') return t('login.phoneNotConfigured')
      if (err.code === 'email_not_configured') return t('login.emailCodeNotConfigured')
      return err.message
    }
    return t('common.somethingWentWrong')
  }

  async function handleGoogleLogin() {
    setError(null)
    const clientId = import.meta.env.VITE_GOOGLE_CLIENT_ID as string | undefined
    if (!clientId) {
      setError(t('login.googleNotConfigured'))
      return
    }
    try {
      await loadGoogleScript()
      window.google!.accounts.id.initialize({
        client_id: clientId,
        callback: async (resp) => {
          try {
            const tokens = await api.googleLogin(resp.credential)
            setTokens(tokens.access_token, tokens.refresh_token)
            navigate('/')
          } catch (err) {
            setError(friendlyError(err))
          }
        },
      })
      window.google!.accounts.id.prompt()
    } catch {
      setError(t('common.somethingWentWrong'))
    }
  }

  async function sendCode() {
    setError(null)
    setCodeBusy(true)
    try {
      await api.sendPhoneCode(phone)
      setCodeSent(true)
    } catch (err) {
      setError(friendlyError(err))
    } finally {
      setCodeBusy(false)
    }
  }

  async function sendEmailVerificationCode() {
    setError(null)
    setEmailCodeBusy(true)
    try {
      await api.sendEmailCode(email)
      setEmailCodeSent(true)
    } catch (err) {
      setError(friendlyError(err))
    } finally {
      setEmailCodeBusy(false)
    }
  }

  async function submitPhone(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const tokens = await api.verifyPhoneCode(phone, phoneCode)
      setTokens(tokens.access_token, tokens.refresh_token)
      navigate('/')
    } catch (err) {
      setError(friendlyError(err))
    } finally {
      setBusy(false)
    }
  }

  // §07/09's "登录页缺中英/亮暗切换" gap — every other page reaches these
  // through Rail (EN/中 button) or /settings, both of which only render
  // inside AppShell; Login is the one screen that never mounts AppShell,
  // so a visitor stuck on the wrong language/theme here had no way to fix
  // either before landing an account. Self-contained rather than pulling
  // in Rail, which assumes a nav rail's worth of surrounding layout this
  // page doesn't have.
  const lang = (i18n.language === 'en' ? 'en' : 'zh') as Lang
  const [theme, setTheme] = useState<Theme>(getStoredTheme())

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const tokens = mode === 'login' ? await api.login(email, password) : await api.register(email, password, emailCode)
      setTokens(tokens.access_token, tokens.refresh_token)
      navigate('/')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t('common.somethingWentWrong'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="min-h-screen bg-zinc-950 lg:grid lg:grid-cols-2">
      <div className="fixed right-4 top-4 z-10 flex gap-2">
        <button
          onClick={() => setStoredLang(lang === 'zh' ? 'en' : 'zh')}
          title={t('rail.switchLanguage')}
          className="flex h-8 w-8 items-center justify-center rounded-full border border-zinc-800 text-[10px] font-medium text-zinc-500 transition hover:border-zinc-700 hover:text-zinc-300"
        >
          {lang === 'zh' ? 'EN' : '中'}
        </button>
        <button
          onClick={() => {
            const next = theme === 'light' ? 'dark' : 'light'
            setTheme(next)
            setStoredTheme(next)
          }}
          title={t('settings.appearance')}
          className="flex h-8 items-center justify-center rounded-full border border-zinc-800 px-2.5 text-[10px] font-medium text-zinc-500 transition hover:border-zinc-700 hover:text-zinc-300"
        >
          {theme === 'light' ? t('settings.themeDark') : t('settings.themeLight')}
        </button>
      </div>
      <div className="relative flex min-h-screen items-center justify-center overflow-hidden p-6 lg:min-h-0">
        {/* Ambient glow — the app has no logo asset, so a soft brand-colored
            wash behind the card is the lightest-touch way to keep this side
            from reading as a bare, un-designed void. */}
        <div className="pointer-events-none absolute inset-0 overflow-hidden">
          <div className="absolute left-1/2 top-[28%] h-[520px] w-[520px] -translate-x-1/2 -translate-y-1/2 rounded-full bg-violet-600/20 blur-[140px]" />
        </div>

        <div className="relative w-full max-w-sm space-y-5">
          <div className="text-center">
            <img src="/logo-mark.png" alt="" className="mx-auto mb-3 h-12 w-12 object-contain" />
            <h1 className="text-xl font-semibold text-zinc-50">{t('brand.name')}</h1>
            <p className="mt-1 text-sm text-zinc-500">{t('brand.tagline')}</p>
          </div>

          <div className="rounded-2xl border border-zinc-800 bg-zinc-900/90 p-6 shadow-2xl shadow-black/40">
            {method === 'email' && (
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
            )}

            {method === 'email' ? (
              <form onSubmit={submit}>
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
                  {mode === 'register' && (
                    <div className="flex gap-2">
                      <input
                        type="text"
                        placeholder={t('login.codePlaceholder')}
                        value={emailCode}
                        onChange={(e) => setEmailCode(e.target.value)}
                        className="w-full min-w-0 flex-1 rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2.5 text-zinc-50 outline-none transition focus:border-violet-500"
                      />
                      <button
                        type="button"
                        onClick={sendEmailVerificationCode}
                        disabled={emailCodeBusy || !email}
                        className="shrink-0 rounded-lg border border-zinc-700 px-3 py-2.5 text-sm text-zinc-300 transition hover:border-violet-500 disabled:opacity-50"
                      >
                        {emailCodeSent ? t('login.codeSent') : t('login.sendCode')}
                      </button>
                    </div>
                  )}
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
            ) : (
              <form onSubmit={submitPhone}>
                <div className="space-y-3">
                  <input
                    type="tel"
                    required
                    placeholder={t('login.phoneNumberPlaceholder')}
                    value={phone}
                    onChange={(e) => setPhone(e.target.value)}
                    className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2.5 text-zinc-50 outline-none transition focus:border-violet-500"
                  />
                  <div className="flex gap-2">
                    <input
                      type="text"
                      required
                      placeholder={t('login.codePlaceholder')}
                      value={phoneCode}
                      onChange={(e) => setPhoneCode(e.target.value)}
                      className="w-full min-w-0 flex-1 rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2.5 text-zinc-50 outline-none transition focus:border-violet-500"
                    />
                    <button
                      type="button"
                      onClick={sendCode}
                      disabled={codeBusy || !phone}
                      className="shrink-0 rounded-lg border border-zinc-700 px-3 py-2.5 text-sm text-zinc-300 transition hover:border-violet-500 disabled:opacity-50"
                    >
                      {codeSent ? t('login.codeSent') : t('login.sendCode')}
                    </button>
                  </div>
                </div>

                {error && <p className="mt-3 text-sm text-red-400">{error}</p>}

                <button
                  type="submit"
                  disabled={busy}
                  className="mt-4 w-full rounded-lg bg-violet-500 px-3 py-2.5 font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
                >
                  {busy ? t('common.processing') : t('login.continuePhone')}
                </button>
              </form>
            )}

            <button
              type="button"
              onClick={() => {
                setError(null)
                setMethod(method === 'email' ? 'phone' : 'email')
              }}
              className="mt-3 w-full text-center text-xs text-zinc-500 hover:text-zinc-300"
            >
              {method === 'email' ? t('login.usePhone') : t('login.useEmail')}
            </button>

            <div className="my-4 flex items-center gap-3 text-xs text-zinc-600">
              <div className="h-px flex-1 bg-zinc-800" />
              {t('login.orDivider')}
              <div className="h-px flex-1 bg-zinc-800" />
            </div>

            <button
              type="button"
              onClick={handleGoogleLogin}
              className="flex w-full items-center justify-center gap-2.5 rounded-lg border border-neutral-300 bg-white px-3 py-2.5 text-sm font-medium text-neutral-800 transition hover:border-neutral-400"
            >
              <GoogleLogo className="h-4 w-4" />
              {t('login.continueWithGoogle')}
            </button>
          </div>

          <p className="text-center text-xs text-zinc-600">
            {t('login.notSure')}
            <Link to="/community" className="text-violet-400 hover:text-violet-300">
              {t('login.tryFree')}
            </Link>
          </p>
        </div>
      </div>

      <div className="relative hidden overflow-hidden bg-zinc-900 lg:block">
        <div className="grid h-screen grid-cols-2 gap-3 overflow-hidden p-3">
          <div className="flex flex-col gap-3">
            {SHOWCASE_IMAGES.slice(0, 4).map((name) => (
              <img
                key={name}
                src={`/login-showcase/${name}.jpg`}
                alt=""
                className="aspect-[3/4] w-full flex-1 rounded-xl object-cover"
              />
            ))}
          </div>
          <div className="flex translate-y-8 flex-col gap-3">
            {SHOWCASE_IMAGES.slice(4).map((name) => (
              <img
                key={name}
                src={`/login-showcase/${name}.jpg`}
                alt=""
                className="aspect-[3/4] w-full flex-1 rounded-xl object-cover"
              />
            ))}
          </div>
        </div>
        <div className="pointer-events-none absolute inset-0 bg-gradient-to-r from-zinc-900/50 via-transparent to-transparent" />
        <div className="pointer-events-none absolute inset-x-0 top-0 h-24 bg-gradient-to-b from-zinc-900 to-transparent" />
        <div className="pointer-events-none absolute inset-x-0 bottom-0 h-24 bg-gradient-to-t from-zinc-900 to-transparent" />
      </div>
    </div>
  )
}
