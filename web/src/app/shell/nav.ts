import { Coins, FolderOpen, Images, ListChecks, Palette, Settings, Sparkles, UserRound, UsersRound, type LucideIcon } from 'lucide-react'
import { lastCreateMode } from '../routing'

export interface NavItem {
  key: 'create' | 'tasks' | 'library' | 'projects' | 'characters' | 'presets' | 'community' | 'credits' | 'settings'
  to: () => string
  /** Paths that mark this item as current. */
  match: string[]
  icon: LucideIcon
  auth: boolean
}

export const MAIN_NAV: NavItem[] = [
  { key: 'create', to: () => `/create/${lastCreateMode()}`, match: ['/create'], icon: Sparkles, auth: false },
  { key: 'tasks', to: () => '/jobs', match: ['/jobs'], icon: ListChecks, auth: true },
  { key: 'library', to: () => '/assets', match: ['/assets'], icon: Images, auth: true },
  { key: 'projects', to: () => '/projects', match: ['/projects'], icon: FolderOpen, auth: true },
  { key: 'characters', to: () => '/characters', match: ['/characters'], icon: UserRound, auth: true },
  { key: 'presets', to: () => '/presets', match: ['/presets'], icon: Palette, auth: true },
  { key: 'community', to: () => '/community', match: ['/community'], icon: UsersRound, auth: false },
]

export const FOOTER_NAV: NavItem[] = [
  { key: 'credits', to: () => '/credits', match: ['/credits'], icon: Coins, auth: true },
  { key: 'settings', to: () => '/settings', match: ['/settings'], icon: Settings, auth: true },
]

export function isCurrent(item: NavItem, pathname: string) {
  return item.match.some((m) => pathname === m || pathname.startsWith(m + '/'))
}
