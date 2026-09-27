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
import { Brand } from '../../app/shell/Brand'
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
    <div className="grid min-h-dvh bg-bg text-fg lg:grid-cols-2">
      <aside className="hidden min-w-0 flex-col border-r border-border bg-surface-2 p-8 lg:flex xl:p-12">
        <Brand />
        <div className="my-auto w-full max-w-[680px] self-center py-10">
          <p className="max-w-[540px] text-[36px] leading-tight font-semibold xl:text-[44px]">{t('hero')}</p>
          <p className="mt-4 text-body text-fg-muted">{t('heroBody')}</p>
          <figure className="mt-8">
            <img
              src="/login-showcase/community-comic.jpg"
              alt={t('showcaseAlt')}
              width={1536}
              height={1024}
              className="aspect-[3/2] w-full rounded-dialog object-contain"
            />
            <figcaption className="mt-3 flex flex-wrap justify-between gap-2 text-caption text-fg-muted">
              <span>{t('showcaseTitle')}</span>
              <span>{t('showcaseSource')}</span>
            </figcaption>
          </figure>
          <div className="mt-6 flex items-center gap-4 border-t border-border pt-5">
            <div aria-hidden className="flex shrink-0 gap-2">
              {['character-girl.png', 'character-mascot.jpg', 'character-boy.png'].map((file) => (
                <img key={file} src={`/login-showcase/${file}`} alt="" width={56} height={64} className="h-16 w-14 rounded-control bg-white object-contain p-1" />
              ))}
            </div>
            <p className="text-caption leading-relaxed text-fg-muted">{t('referenceCaption')}</p>
          </div>
        </div>
      </aside>
      <main className="flex min-w-0 flex-col px-5 py-6 sm:px-10">
        <header className="flex items-center justify-between gap-3">
          <div className="lg:invisible"><Brand /></div>
          <LanguageSwitch />
        </header>
        <div className="my-auto w-full max-w-[420px] self-center py-10">
          <h1 className="text-[28px] font-semibold">{t('welcome')}</h1>
          <p className="mt-2 mb-6 text-body text-fg-muted">{t('subtitle')}</p>
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
                      <Field label={t('email')} required error={fieldError('email', emailError)}>
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
                        label={t('password')}
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
                    <Field label={t('phone')} required help={t('phoneHelp')} error={fieldError('phone', phoneError)}>
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
                  <Button className="w-full" loading={busy} onClick={google}>
                    {t('google')}
                  </Button>
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
  )
}
