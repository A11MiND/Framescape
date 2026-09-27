import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { ApiError } from '../../lib/api/client'
import { createApi } from '../../lib/api/create'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { useAuthStore } from '../../lib/authStore'
import { safeReturnPath } from '../../app/routing'
import { LanguageSwitch } from '../../app/shell/TopBar'
import { Button, ErrorState, Field, Input, PasswordInput, SegmentedControl, Skeleton, Tabs, TabList } from '../../ui'

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
      s.onerror = () => {
        googleScriptPromise = null
        s.remove()
        reject(new Error('failed to load Google script'))
      }
      document.head.appendChild(s)
    })
  }
  return googleScriptPromise
}

function LoginBrand({ t }: { t: (key: string) => string }) {
  return (
    <div className="flex items-center gap-3">
      <img src="/logo-mark.png" alt="" className="size-9 shrink-0 object-contain" />
      <span className="whitespace-nowrap text-[18px] font-semibold tracking-tight text-hero-fg sm:text-[22px]">Framescape <span className="font-normal text-hero-muted">{t('brandSecondary')}</span></span>
    </div>
  )
}

function DualLabel({ primary, secondary }: { primary: string; secondary: string }) {
  return (
    <>
      {primary}
      <span className="ml-2 font-normal text-fg-muted">{secondary}</span>
    </>
  )
}

export default function LoginPage() {
  const { t } = useTranslation('authV2')
  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities })
  const [mode, setMode] = useState('login')
  const [method, setMethod] = useState('email')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [phone, setPhone] = useState('')
  const [code, setCode] = useState('')
  const [tried, setTried] = useState(false)
  const [busy, setBusy] = useState(false)
  const [sending, setSending] = useState(false)
  const [remaining, setRemaining] = useState(0)
  const [error, setError] = useState<{ field: string; message: string } | null>(null)
  const navigate = useNavigate()
  const location = useLocation()
  const setTokens = useAuthStore((s) => s.setTokens)
  const auth = caps.data?.auth
  const googleId = import.meta.env.VITE_GOOGLE_CLIENT_ID as string | undefined
  const wantsCode = method === 'phone' || (mode === 'register' && auth?.email_verification)
  const min = caps.data?.password?.min_chars ?? 8
  const max = caps.data?.password?.max_bytes ?? 72
  const emailError = !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim()) ? t('emailError') : undefined
  const phoneError = !/^\+[1-9]\d{7,14}$/.test(phone.trim()) ? t('phoneError') : undefined
  const passwordError = !password
    ? t('required')
    : mode === 'register' && ([...password].length < min || new TextEncoder().encode(password).length > max)
      ? t('passwordRule', { min, max })
      : undefined
  const codeError = wantsCode && !/^\d{6}$/.test(code) ? t('codeError') : undefined
  useEffect(() => {
    if (!remaining) return
    const timer = setTimeout(() => setRemaining((n) => Math.max(0, n - 1)), 1000)
    return () => clearTimeout(timer)
  }, [remaining])
  const finish = (tokens: { access_token: string; refresh_token: string }) => {
    setTokens(tokens.access_token, tokens.refresh_token)
    navigate(safeReturnPath((location.state as { from?: unknown } | null)?.from) ?? '/', { replace: true })
  }
  const report = (err: unknown, fallback: string) => {
    const c = err instanceof ApiError ? err.code : ''
    setError({
      field: c === 'email_taken' ? 'email' : c === 'invalid_code' || c === 'code_expired' ? 'code' : fallback,
      message: errorText(t, err),
    })
  }
  const fieldError = (field: string, local?: string) => (error?.field === field ? error.message : tried ? local : undefined)
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (busy || sending) return
    setTried(true)
    setError(null)
    if ((method === 'email' && (emailError || passwordError)) || (method === 'phone' && phoneError) || codeError) return
    setBusy(true)
    try {
      finish(
        method === 'phone'
          ? await api.verifyPhoneCode(phone.trim(), code)
          : mode === 'login'
            ? await api.login(email.trim(), password)
            : await api.register(email.trim(), password, wantsCode ? code : undefined),
      )
    } catch (err) {
      report(err, method === 'phone' ? 'code' : 'password')
    } finally {
      setBusy(false)
    }
  }
  const send = async () => {
    if (remaining || sending || busy) return
    if (method === 'phone' ? phoneError : emailError) {
      setError({ field: method === 'phone' ? 'phone' : 'email', message: (method === 'phone' ? phoneError : emailError)! })
      return
    }
    setSending(true)
    setError(null)
    try {
      if (method === 'phone') await api.sendPhoneCode(phone.trim())
      else await api.sendEmailCode(email.trim())
      setRemaining(60)
    } catch (err) {
      report(err, 'code')
    } finally {
      setSending(false)
    }
  }
  const google = async () => {
    if (!googleId || busy) return
    setBusy(true)
    setError(null)
    try {
      await loadGoogleScript()
      window.google!.accounts.id.initialize({
        client_id: googleId,
        callback: async (r) => {
          setBusy(true)
          try {
            finish(await api.googleLogin(r.credential))
          } catch (err) {
            report(err, 'form')
          } finally {
            setBusy(false)
          }
        },
      })
      window.google!.accounts.id.prompt()
    } catch (err) {
      report(err, 'form')
    } finally {
      setBusy(false)
    }
  }
  const switchMode = (v: string) => {
    setMode(v)
    setTried(false)
    setError(null)
  }
  return (
    <div className="min-h-dvh bg-surface-2 p-2 text-fg sm:p-3 lg:p-4">
      <div className="grid min-h-[calc(100dvh-1rem)] overflow-hidden rounded-dialog border border-border bg-surface sm:min-h-[calc(100dvh-1.5rem)] lg:grid-cols-[58%_42%] lg:min-h-[calc(100dvh-2rem)]">
      <aside className="relative hidden min-w-0 overflow-hidden bg-[#edf6ff] lg:block dark:bg-surface-2">
        <img src="/login-showcase/creative-collage.png" alt={t('showcaseAlt')} width={1536} height={1024} className="absolute inset-0 size-full object-cover" />
        <div className="absolute inset-0 bg-gradient-to-b from-white/10 via-transparent to-[#edf6ff]/20 dark:from-black/5 dark:to-surface-2/20" />
        <div className="relative z-10 max-w-[440px] p-8 xl:p-12 dark:rounded-dialog dark:bg-surface/80 dark:backdrop-blur-sm">
          <LoginBrand t={t} />
          <p className="mt-8 text-[38px] leading-[1.12] font-semibold tracking-tight text-hero-fg xl:text-[48px]">{t('hero')}</p>
          <p className="mt-2 text-[22px] leading-tight text-hero-fg xl:text-[28px]">{t('heroSecondary')}</p>
          <p className="mt-5 text-body leading-relaxed text-hero-muted">{t('heroBody')}</p>
          <p className="mt-1 text-caption text-hero-muted">{t('heroBodySecondary')}</p>
        </div>
      </aside>
      <main className="flex min-w-0 flex-col bg-surface px-5 py-6 sm:px-10 lg:px-10 xl:px-14">
        <header className="flex flex-wrap items-center justify-between gap-3 lg:justify-end">
          <div className="lg:hidden"><LoginBrand t={t} /></div>
          <LanguageSwitch />
        </header>
        <div className="my-auto w-full max-w-[440px] self-center py-10 lg:my-0 lg:py-0">
          <h1 className="text-[30px] font-semibold tracking-tight">{t('welcome')}</h1>
          <p className="mt-1 text-[21px] leading-tight text-fg-muted">{t('welcomeSecondary')}</p>
          <p className="mt-5 text-body text-fg-muted">{t('subtitle')}</p>
          <p className="mt-1 text-caption text-fg-muted">{t('subtitleSecondary')}</p>
          {caps.isPending ? (
            <Skeleton className="h-72" />
          ) : caps.isError ? (
            <ErrorState message={errorText(t, caps.error)} onRetry={() => caps.refetch()} />
          ) : (
            <>
              {auth?.email_password && auth?.phone_sms && (
                <SegmentedControl
                  label={t('method')}
                  value={method}
                  onChange={(v) => {
                    setMethod(v)
                    setTried(false)
                    setError(null)
                    setCode('')
                  }}
                  options={[
                    { value: 'email', label: t('emailMethod') },
                    { value: 'phone', label: t('phoneMethod') },
                  ]}
                />
              )}
              {(method === 'email' && auth?.email_password) || (method === 'phone' && auth?.phone_sms) ? (
                <form onSubmit={submit} noValidate className="mt-5 space-y-4">
                  {method === 'email' ? (
                    <>
                      <Tabs value={mode} onValueChange={switchMode}>
                        <TabList
                          label={t('mode')}
                          items={[
                            { value: 'login', label: t('login') },
                            { value: 'register', label: t('register') },
                          ]}
                        />
                      </Tabs>
                      <Field label={<DualLabel primary={t('email')} secondary={t('emailSecondary')} />} required error={fieldError('email', emailError)}>
                        <Input
                          type="email"
                          autoComplete="email"
                          value={email}
                          onChange={(e) => {
                            setEmail(e.target.value)
                            setError(null)
                          }}
                          disabled={busy || sending}
                        />
                      </Field>
                      <Field
                        label={<DualLabel primary={t('password')} secondary={t('passwordSecondary')} />}
                        required
                        help={mode === 'register' ? t('passwordRule', { min, max }) : undefined}
                        error={fieldError('password', passwordError)}
                      >
                        <PasswordInput
                          autoComplete={mode === 'register' ? 'new-password' : 'current-password'}
                          value={password}
                          onChange={(e) => {
                            setPassword(e.target.value)
                            setError(null)
                          }}
                          disabled={busy}
                        />
                      </Field>
                    </>
                  ) : (
                    <Field label={<DualLabel primary={t('phone')} secondary={t('phoneSecondary')} />} required help={t('phoneHelp')} error={fieldError('phone', phoneError)}>
                      <Input
                        type="tel"
                        autoComplete="tel"
                        value={phone}
                        onChange={(e) => {
                          setPhone(e.target.value)
                          setError(null)
                        }}
                        disabled={busy || sending}
                      />
                    </Field>
                  )}
                  {wantsCode && (
                    <Field label={t('code')} required error={fieldError('code', codeError)}>
                      <div className="flex gap-2">
                        <Input
                          inputMode="numeric"
                          autoComplete="one-time-code"
                          maxLength={6}
                          value={code}
                          onChange={(e) => {
                            setCode(e.target.value)
                            setError(null)
                          }}
                        />
                        <Button onClick={send} loading={sending} disabled={remaining > 0 || busy}>
                          {remaining ? t('resend', { n: remaining }) : t('send')}
                        </Button>
                      </div>
                    </Field>
                  )}
                  <Button type="submit" variant="primary" loading={busy} disabled={sending} className="w-full">
                    {t(method === 'phone' ? 'phoneSubmit' : mode)}
                  </Button>
                </form>
              ) : auth?.phone_sms ? (
                <Button onClick={() => setMethod('phone')}>{t('phoneMethod')}</Button>
              ) : null}
              {auth?.google && googleId && (
                <div className="mt-5 border-t border-border pt-5">
                  <p className="mb-3 text-center text-caption text-fg-muted">{t('or')}</p>
                  <div className={auth.phone_sms ? 'grid gap-3 sm:grid-cols-2' : ''}>
                    {auth.phone_sms && <Button className="w-full" disabled={busy} onClick={() => setMethod('phone')}>{t('phoneMethod')}</Button>}
                    <Button className="w-full" loading={busy} onClick={google}>{t('google')}</Button>
                  </div>
                </div>
              )}
              {error?.field === 'form' && (
                <p role="alert" className="mt-3 text-danger-fg">
                  {error.message}
                </p>
              )}
            </>
          )}
          <nav aria-label={t('guest')} className="mt-7 flex flex-wrap justify-center gap-5 text-body text-primary-text">
            {auth?.guest_trial && <Link to="/create/image">{t('trial')}</Link>}
            <Link to="/community">{t('community')}</Link>
          </nav>
        </div>
      </main>
      </div>
    </div>
  )
}
