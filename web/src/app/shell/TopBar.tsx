import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Coins, WifiOff } from 'lucide-react'
import { Button, Menu, SegmentedControl, cn } from '../../ui'
import { useAuthStore } from '../../lib/authStore'
import type { Me } from '../../lib/api/account'
import { projectsApi } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { useStream } from '../../lib/stream/context'
import { formatClock, formatNumber } from '../../lib/format'
import { setStoredLang, type Lang } from '../../i18n'
import { useCurrentProject } from '../currentProject'
import { AccountAvatar } from './AccountAvatar'
import { Brand } from './Brand'
import NotificationCenter from '../../components/NotificationCenter'
import type { JobCounts } from '../../lib/api/jobs'

function CountLink({ to, label, tone }: { to: string; label: string; tone: 'primary' | 'warning' }) {
  return (
    <Link
      to={to}
      className={cn(
        'inline-flex h-8 items-center rounded-full px-3 text-caption font-medium whitespace-nowrap tabular-nums',
        tone === 'warning' ? 'bg-warning-soft text-warning-fg' : 'bg-primary-soft text-primary-text',
      )}
    >
      {label}
    </Link>
  )
}

export function LanguageSwitch() {
  const { t, i18n } = useTranslation('shell')
  return (
    <SegmentedControl<Lang>
      label={t('top.language')}
      value={i18n.language as Lang}
      onChange={setStoredLang}
      options={[
        { value: 'zh', label: t('lang.zhCN') },
        { value: 'zh-TW', label: t('lang.zhTW') },
        { value: 'en', label: t('lang.en') },
      ]}
    />
  )
}

export function TopBar({ me, counts, countsFailed }: { me: Me | undefined; counts: JobCounts | undefined; countsFailed: boolean }) {
  const { t, i18n } = useTranslation('shell')
  const navigate = useNavigate()
  const qc = useQueryClient()
  const signedIn = useAuthStore((s) => Boolean(s.accessToken))
  const logout = useAuthStore((s) => s.logout)
  const stream = useStream()
  const [project, setProject] = useCurrentProject(me?.biz_id)
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list, enabled: signedIn })

  const count = (n: number | undefined) => (countsFailed || n === undefined ? '--' : formatNumber(n, i18n.language))
  const signOut = () => {
    logout()
    qc.clear()
    navigate('/login')
  }

  return (
    <header className="flex h-topbar shrink-0 items-center gap-3 border-b border-border bg-surface px-4 lg:px-6">
      <Link to="/" className="lg:hidden" aria-label={t('brand')}>
        <Brand compact />
      </Link>
      {signedIn && (
        <label className="flex min-w-0 items-center gap-2">
          <span className="hidden text-caption text-fg-muted xl:inline">{t('top.currentProject')}</span>
          <select
            value={project ?? ''}
            onChange={(e) => setProject(e.target.value || null)}
            aria-label={t('top.currentProject')}
            className="h-9 max-w-[200px] min-w-0 truncate rounded-card border border-border-control bg-surface px-2.5 text-body text-fg"
          >
            <option value="">{t('top.allProjects')}</option>
            {(projects.data?.projects ?? []).map((p) => (
              <option key={p.biz_id} value={p.biz_id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
      )}
      <div className="ml-auto flex items-center gap-2">
        {signedIn && stream.status === 'offline' && (
          <span role="status" className="hidden items-center gap-1.5 text-caption text-warning-fg md:inline-flex">
            <WifiOff aria-hidden className="size-4 text-warning" />
            {stream.lastSyncAt ? t('stream.offline', { time: formatClock(stream.lastSyncAt, i18n.language) }) : t('stream.reconnecting')}
          </span>
        )}
        {signedIn && (
          <div className="hidden items-center gap-2 sm:flex">
            <CountLink to="/jobs?bucket=active" tone="primary" label={t('top.running', { n: count(counts?.active) })} />
            <CountLink to="/jobs?bucket=needs_review" tone="warning" label={t('top.needsReview', { n: count(counts?.needs_review) })} />
          </div>
        )}
        {signedIn && me && (
          <Link to="/credits" className="hidden h-8 items-center gap-1.5 rounded-full px-2.5 text-caption font-medium text-fg hover:bg-surface-2 md:inline-flex">
            <Coins aria-hidden className="size-4 text-fg-muted" />
            <span className="tabular-nums">{t('top.credits', { n: formatNumber(me.balance, i18n.language) })}</span>
          </Link>
        )}
        <div className="hidden md:block">
          <LanguageSwitch />
        </div>
        {signedIn && <NotificationCenter />}
        {signedIn ? (
          <Menu
            trigger={
              <button type="button" aria-label={t('top.account')} className="inline-flex size-10 items-center justify-center rounded-full text-fg-muted hover:bg-surface-2 hover:text-fg">
                <AccountAvatar url={me?.avatar_url} className="size-8" />
              </button>
            }
            items={[
              { key: 'settings', label: t('nav.settings'), onSelect: () => navigate('/settings') },
              ...(me?.is_admin ? [{ key: 'admin', label: t('top.admin'), onSelect: () => navigate('/admin/spend') }] : []),
              { key: 'signout', label: t('top.signOut'), onSelect: signOut, danger: true },
            ]}
          />
        ) : (
          <Button variant="primary" size="sm" onClick={() => navigate('/login')}>
            {t('top.signIn')}
          </Button>
        )}
      </div>
    </header>
  )
}
