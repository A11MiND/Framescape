import { useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import { cn, Tooltip } from '../../ui'
import { useAuthStore } from '../../lib/authStore'
import { Brand } from './Brand'
import { FOOTER_NAV, MAIN_NAV, isCurrent, type NavItem } from './nav'

const COLLAPSED_KEY = 'aigc.nav.collapsed'

function readCollapsed() {
  try {
    return localStorage.getItem(COLLAPSED_KEY) === '1'
  } catch {
    return false
  }
}

export function Sidebar({ needsReview }: { needsReview: number | null }) {
  const { t } = useTranslation('shell')
  const { pathname } = useLocation()
  const signedIn = useAuthStore((s) => Boolean(s.accessToken))
  const [collapsed, setCollapsed] = useState(readCollapsed)
  const toggle = () => {
    setCollapsed((c) => {
      try {
        localStorage.setItem(COLLAPSED_KEY, c ? '0' : '1')
      } catch {
        // not remembered
      }
      return !c
    })
  }

  const link = (item: NavItem) => {
    const current = isCurrent(item, pathname)
    const label = t(`nav.${item.key}`)
    const badge = item.key === 'tasks' && needsReview ? needsReview : 0
    const el = (
      <Link
        to={item.to()}
        aria-current={current ? 'page' : undefined}
        aria-label={collapsed ? label : undefined}
        className={cn(
          'relative flex h-10 items-center gap-3 rounded-card px-3 text-body font-medium transition-colors',
          current ? 'bg-primary-soft text-primary-text' : 'text-fg-muted hover:bg-surface-2 hover:text-fg',
          collapsed && 'justify-center px-0',
        )}
      >
        <item.icon aria-hidden className="size-5 shrink-0" />
        {!collapsed && <span className="truncate">{label}</span>}
        {badge > 0 && (
          <span
            className={cn(
              'rounded-full bg-warning-soft px-1.5 text-badge font-semibold text-warning-fg tabular-nums',
              collapsed ? 'absolute top-1 right-1.5' : 'ml-auto',
            )}
          >
            {badge}
          </span>
        )}
      </Link>
    )
    return (
      <li key={item.key}>
        {collapsed ? (
          <Tooltip content={label} side="right">
            {el}
          </Tooltip>
        ) : (
          el
        )}
      </li>
    )
  }

  const visible = (items: NavItem[]) => items.filter((i) => signedIn || !i.auth)
  return (
    <nav
      aria-label={t('nav.label')}
      className={cn('hidden shrink-0 flex-col border-r border-border bg-surface lg:flex', collapsed ? 'w-nav-collapsed' : 'w-nav')}
    >
      <div className={cn('flex h-topbar items-center', collapsed ? 'justify-center' : 'px-5')}>
        <Link to="/" aria-label={t('brand')}>
          <Brand compact={collapsed} />
        </Link>
      </div>
      <ul className="flex flex-col gap-1 px-3 pt-2">{visible(MAIN_NAV).map(link)}</ul>
      <div className="mt-auto flex flex-col gap-1 px-3 pb-3">
        <ul className="flex flex-col gap-1">{visible(FOOTER_NAV).map(link)}</ul>
        <button
          type="button"
          onClick={toggle}
          aria-label={collapsed ? t('nav.expand') : t('nav.collapse')}
          aria-expanded={!collapsed}
          className={cn(
            'flex h-10 items-center gap-3 rounded-card px-3 text-body text-fg-muted hover:bg-surface-2 hover:text-fg',
            collapsed && 'justify-center px-0',
          )}
        >
          {collapsed ? <PanelLeftOpen aria-hidden className="size-5" /> : <PanelLeftClose aria-hidden className="size-5" />}
          {!collapsed && <span>{t('nav.collapse')}</span>}
        </button>
      </div>
    </nav>
  )
}
