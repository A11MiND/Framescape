import type { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '../lib/authStore'

// Admin's own minimal chrome — deliberately NOT AppShell/Rail. The regular
// app's nav (工坊/作业/资产/项目/角色/预设/社区) is a creative-tool surface
// with nothing to do with reviewing usage or managing accounts; reusing it
// here would mean an admin reviewing users sits one accidental click away
// from the image-generation UI, and a management table crammed under the
// same sidebar reads as a bolted-on tab rather than its own surface. Kept
// intentionally light — a thin header with a way back to the app and to
// sign out, nothing else — since /admin is the only route this shell backs.
export default function AdminShell({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const logout = useAuthStore((s) => s.logout)

  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-50">
      <header className="flex items-center justify-between border-b border-zinc-800 px-6 py-3">
        <div className="flex items-center gap-3">
          <img src="/logo-mark.png" alt="" className="h-7 w-7 object-contain" />
          <span className="text-sm font-medium text-zinc-300">{t('admin.title')}</span>
        </div>
        <div className="flex items-center gap-4 text-sm">
          <NavLink to="/" className="text-zinc-400 transition hover:text-zinc-200">
            {t('admin.backToApp')}
          </NavLink>
          <button onClick={logout} className="text-zinc-400 transition hover:text-zinc-200">
            {t('rail.logout')}
          </button>
        </div>
      </header>
      <div className="mx-auto max-w-6xl px-6 py-8">{children}</div>
    </div>
  )
}
