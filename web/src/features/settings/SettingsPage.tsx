import { useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CircleCheck, Globe, LogOut, Monitor, Palette, ShieldCheck, UserRound } from 'lucide-react'
import { Button, Card, Field, Input, PageHeader, PasswordInput, cn } from '../../ui'
import { accountApi } from '../../lib/api/account'
import { createApi } from '../../lib/api/create'
import { ApiError } from '../../lib/api/client'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { formatClock, formatDate, formatNumber } from '../../lib/format'
import { setThemePreference, useThemePreference, type ThemePreference } from '../../lib/theme'
import { setStoredLang, type Lang } from '../../i18n'
import { useAuthStore } from '../../lib/authStore'
import { useMe } from '../../app/useMe'

const utf8Bytes = (s: string) => new TextEncoder().encode(s).length

/** A guide only: length and variety of character kinds. */
function strength(p: string): 'weak' | 'fair' | 'good' {
  const kinds = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z\d]/].filter((r) => r.test(p)).length
  if ([...p].length >= 12 && kinds >= 3) return 'good'
  if ([...p].length >= 8 && kinds >= 2) return 'fair'
  return 'weak'
}

function Section({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <Card className="flex flex-col gap-4">
      <h2 className="flex items-center gap-2 text-body font-semibold text-fg">
        <span aria-hidden className="text-fg-muted">
          {icon}
        </span>
        {title}
      </h2>
      {children}
    </Card>
  )
}

function PasswordForm({ min, max }: { min: number; max: number }) {
  const { t } = useTranslation('settings')
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [tried, setTried] = useState(false)
  const [wrongCurrent, setWrongCurrent] = useState(false)
  const [saved, setSaved] = useState(false)
  const save = useMutation({
    mutationFn: () => accountApi.changePassword(current, next),
    onSuccess: () => {
      setSaved(true)
      setCurrent('')
      setNext('')
      setConfirm('')
      setTried(false)
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === 'invalid_credentials') setWrongCurrent(true)
    },
  })
  const nextError = [...next].length < min ? t('security.tooShort', { min }) : utf8Bytes(next) > max ? t('security.tooLong') : undefined
  const confirmError = confirm !== next ? t('security.mismatch') : undefined
  const currentError = wrongCurrent ? t('security.wrongCurrent') : !current ? t('security.currentRequired') : undefined
  const level = strength(next)
  const submit = () => {
    setTried(true)
    setSaved(false)
    if (!currentError && !nextError && !confirmError) save.mutate()
  }
  const otherError = save.error && !(save.error instanceof ApiError && save.error.code === 'invalid_credentials') ? errorText(t, save.error) : undefined

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <Field label={t('security.current')} error={tried && currentError ? currentError : undefined}>
        <PasswordInput
          autoComplete="current-password"
          value={current}
          onChange={(e) => {
            setCurrent(e.target.value)
            setWrongCurrent(false)
          }}
        />
      </Field>
      <Field label={t('security.new')} help={t('security.rules', { min, max })} error={tried && nextError ? nextError : undefined}>
        <PasswordInput autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
      </Field>
      {next && (
        <div className="flex flex-col gap-1">
          <div className="flex items-center gap-2 text-caption">
            <span className="text-fg-muted">{t('security.strength.label')}</span>
            <span className="flex flex-1 gap-1" aria-hidden>
              {[0, 1, 2].map((i) => (
                <span key={i} className={cn('h-1.5 flex-1 rounded-full', i <= ['weak', 'fair', 'good'].indexOf(level) ? (level === 'good' ? 'bg-success' : level === 'fair' ? 'bg-warning' : 'bg-danger') : 'bg-surface-2')} />
              ))}
            </span>
            <span className="font-medium text-fg">{t(`security.strength.${level}`)}</span>
          </div>
          <p className="text-caption text-fg-muted">{t('security.strengthNote')}</p>
        </div>
      )}
      <Field label={t('security.confirm')} error={tried && confirmError ? confirmError : undefined}>
        <PasswordInput autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
      </Field>
      {otherError && (
        <p role="alert" className="text-caption text-danger-fg">
          {otherError}
        </p>
      )}
      {saved && (
        <p role="status" className="flex items-center gap-1.5 rounded-card bg-success-soft px-3 py-2 text-caption text-success-fg">
          <CircleCheck aria-hidden className="size-4" />
          {t('security.saved')}
        </p>
      )}
      <Button type="submit" variant="primary" loading={save.isPending}>
        {t('security.save')}
      </Button>
    </form>
  )
}

// Fixed colours: each preview shows its own theme whatever the current one is.
const THUMB = {
  light: { frame: '#e5e7eb', bg: '#f7f8fa', side: '#ffffff', line: '#e5e7eb', block: '#e5e7eb' },
  dark: { frame: '#3f3f46', bg: '#18181b', side: '#27272a', line: '#52525b', block: '#3f3f46' },
}

/** A miniature of the interface in one theme, for the theme picker. */
function ThemeThumb({ dark }: { dark: boolean }) {
  const c = THUMB[dark ? 'dark' : 'light']
  return (
    <span aria-hidden className="flex h-16 w-full overflow-hidden rounded-[6px] border" style={{ borderColor: c.frame, background: c.bg }}>
      <span className="flex w-1/4 flex-col gap-1 p-1.5" style={{ background: c.side }}>
        {[0, 1, 2].map((i) => (
          <span key={i} className="h-1 rounded-full" style={{ background: c.line }} />
        ))}
      </span>
      <span className="flex flex-1 flex-col gap-1 p-1.5">
        <span className="h-5 rounded-[3px]" style={{ background: c.block }} />
        <span className="h-1 w-2/3 rounded-full bg-primary" />
      </span>
    </span>
  )
}

/** Settings (spec P19): account, password, appearance and language; switching keeps what is being edited. */
export default function SettingsPage() {
  const { t, i18n } = useTranslation('settings')
  const lang = i18n.language === 'en' ? 'en' : 'zh'
  const navigate = useNavigate()
  const qc = useQueryClient()
  const logout = useAuthStore((s) => s.logout)
  const me = useMe()
  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities, staleTime: 60_000 })
  const [pref, resolved] = useThemePreference()
  const login = me.data?.email ?? me.data?.phone ?? ''
  const initial = (me.data?.email ?? '').trim().charAt(0).toUpperCase() || (me.data?.phone ?? '').slice(-2)
  const sample = new Date(Date.UTC(2026, 8, 26, 6, 20))
  const signOut = () => {
    logout()
    qc.clear()
    navigate('/login')
  }

  return (
    <div className="mx-auto flex max-w-[1200px] flex-col gap-5 px-4 py-6 lg:px-6">
      <PageHeader title={t('title')} description={t('description')} />
      <div className="grid items-start gap-4 md:grid-cols-2">
        <Section icon={<UserRound className="size-5" />} title={t('account.title')}>
          <div className="flex items-center gap-3">
            <span aria-hidden className="flex size-12 items-center justify-center rounded-full bg-primary-soft text-title font-semibold text-primary-text">
              {initial || <UserRound className="size-6" />}
            </span>
            <span className="min-w-0 truncate text-body font-medium text-fg">{login}</span>
          </div>
          {login && (
            <Field label={me.data?.email ? t('account.email') : t('account.phone')} help={t('account.emailHelp')}>
              <Input value={login} readOnly />
            </Field>
          )}
          <Button variant="danger-outline" icon={<LogOut aria-hidden className="size-4" />} onClick={signOut}>
            {t('account.signOut')}
          </Button>
        </Section>

        <Section icon={<ShieldCheck className="size-5" />} title={t('security.title')}>
          {me.data && me.data.has_password === false ? (
            <p className="text-body text-fg-muted">{t('security.noPassword')}</p>
          ) : (
            <PasswordForm min={caps.data?.password?.min_chars ?? 8} max={caps.data?.password?.max_bytes ?? 72} />
          )}
        </Section>

        <Section icon={<Palette className="size-5" />} title={t('appearance.title')}>
          <div role="radiogroup" aria-label={t('appearance.label')} className="grid grid-cols-3 gap-2">
            {(['light', 'dark', 'system'] as ThemePreference[]).map((p) => (
              <button
                key={p}
                type="button"
                role="radio"
                aria-checked={pref === p}
                onClick={() => setThemePreference(p)}
                className={cn('flex flex-col gap-2 rounded-card border p-2 text-left', pref === p ? 'border-primary ring-1 ring-primary' : 'border-border hover:border-border-control')}
              >
                {p === 'system' ? (
                  <span className="flex gap-1">
                    <ThemeThumb dark={false} />
                    <ThemeThumb dark />
                  </span>
                ) : (
                  <ThemeThumb dark={p === 'dark'} />
                )}
                <span className="flex items-center gap-1 text-caption font-medium text-fg">
                  {p === 'system' && <Monitor aria-hidden className="size-3.5" />}
                  {t(`appearance.${p}`)}
                </span>
              </button>
            ))}
          </div>
          {pref === 'system' && <p className="text-caption text-fg-muted">{t('appearance.systemNow', { theme: t(`appearance.${resolved}`) })}</p>}
          <p className="text-caption text-fg-muted">{t('appearance.help')}</p>
        </Section>

        <Section icon={<Globe className="size-5" />} title={t('language.title')}>
          <div role="radiogroup" aria-label={t('language.label')} className="flex flex-col gap-2">
            {(['zh', 'en'] as Lang[]).map((l) => (
              <button
                key={l}
                type="button"
                role="radio"
                aria-checked={lang === l}
                onClick={() => setStoredLang(l)}
                className={cn('flex h-11 items-center gap-3 rounded-card border px-3 text-body', lang === l ? 'border-primary bg-primary-soft text-primary-text' : 'border-border text-fg hover:border-border-control')}
              >
                <span aria-hidden className={cn('size-4 rounded-full border-2', lang === l ? 'border-primary bg-primary shadow-[inset_0_0_0_2px_var(--color-surface)]' : 'border-border-control')} />
                {t(`language.names.${l}`)}
              </button>
            ))}
          </div>
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 rounded-card bg-surface-2 p-3 text-caption">
            <dt className="text-fg-muted">{t('language.date')}</dt>
            <dd className="text-fg tabular-nums">{formatDate(sample, i18n.language)}</dd>
            <dt className="text-fg-muted">{t('language.time')}</dt>
            <dd className="text-fg tabular-nums">{formatClock(sample, i18n.language)}</dd>
            <dt className="text-fg-muted">{t('language.number')}</dt>
            <dd className="text-fg tabular-nums">{formatNumber(1234567.89, i18n.language)}</dd>
          </dl>
          <p className="text-caption text-fg-muted">{t('language.note')}</p>
        </Section>
      </div>
    </div>
  )
}
