import { useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { WORKFLOW_LABEL_KEY, type Tab } from '../lib/jobResult'
import { useClickOutside } from '../hooks/useClickOutside'

// §07/09's "通知中心 + 數字角標" gap — the running-job badge Rail already
// had (elsewhere in this file) only ever pointed at the plain job list; R12
// specifically wants a way to surface "有作業等待你決策" (a suspended
// preview gate) without the user having to go looking for it. This adds an
// actual dropdown backed by the same GET /jobs the badge already polls, no
// new endpoint — split into the two things worth surfacing: jobs genuinely
// waiting on a decision (suspended), and recently finished ones (so a
// completed generation doesn't just silently sit in the job list either).
export default function NotificationCenter() {
  const { t, i18n } = useTranslation()
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  useClickOutside(rootRef, () => setOpen(false), open)

  const suspended = useQuery({
    queryKey: ['jobs', 'notif-suspended'],
    queryFn: () => api.listJobs({ status: 'suspended', limit: 5 }),
    refetchInterval: 15_000,
  })
  const recent = useQuery({
    queryKey: ['jobs', 'notif-recent'],
    queryFn: () => api.listJobs({ status: 'succeeded', limit: 5 }),
    enabled: open,
  })

  const suspendedJobs = suspended.data?.jobs ?? []
  const count = suspendedJobs.length

  function formatTime(iso: string) {
    return new Date(iso).toLocaleString(i18n.language === 'en' ? 'en-US' : 'zh-CN', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  }

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        title={t('notifications.title')}
        className="relative flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-zinc-800 text-zinc-500 transition hover:border-zinc-700 hover:text-zinc-300"
      >
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" className="h-4 w-4">
          <path d="M5 8a5 5 0 0 1 10 0c0 3.2 1 4.6 1.5 5.2a.6.6 0 0 1-.5 1H4a.6.6 0 0 1-.5-1C4 12.6 5 11.2 5 8Z" />
          <path d="M8.2 16.5a1.8 1.8 0 0 0 3.6 0" />
        </svg>
        {count > 0 && (
          <span className="absolute -right-1 -top-1 rounded-full bg-amber-500 px-1 text-[9px] font-medium leading-[14px] text-black">
            {count}
          </span>
        )}
      </button>
      {open && (
        <div className="absolute bottom-full left-0 z-20 mb-2 w-72 rounded-xl border border-zinc-800 bg-zinc-900 p-2 shadow-xl lg:bottom-auto lg:left-full lg:top-0 lg:ml-2 lg:mb-0">
          <p className="px-1.5 pb-1 pt-1 text-[11px] uppercase tracking-wide text-zinc-500">{t('notifications.awaitingDecision')}</p>
          {suspendedJobs.length === 0 && <p className="px-1.5 py-2 text-xs text-zinc-600">{t('notifications.none')}</p>}
          {suspendedJobs.map((j) => (
            <Link
              key={j.biz_id}
              to={`/jobs/${j.biz_id}`}
              onClick={() => setOpen(false)}
              className="block rounded-lg px-1.5 py-1.5 text-xs text-zinc-200 hover:bg-zinc-800"
            >
              <span className="mr-1 text-amber-400">●</span>
              {j.title || t(WORKFLOW_LABEL_KEY[j.workflow_name as Tab]) || j.workflow_name}
            </Link>
          ))}

          <p className="mt-2 border-t border-zinc-800 px-1.5 pb-1 pt-2 text-[11px] uppercase tracking-wide text-zinc-500">
            {t('notifications.recentlyDone')}
          </p>
          {recent.data?.jobs.length === 0 && <p className="px-1.5 py-2 text-xs text-zinc-600">{t('notifications.none')}</p>}
          {recent.data?.jobs.map((j) => (
            <Link
              key={j.biz_id}
              to={`/jobs/${j.biz_id}`}
              onClick={() => setOpen(false)}
              className="flex items-center justify-between rounded-lg px-1.5 py-1.5 text-xs text-zinc-300 hover:bg-zinc-800"
            >
              <span className="truncate">{j.title || t(WORKFLOW_LABEL_KEY[j.workflow_name as Tab]) || j.workflow_name}</span>
              <span className="shrink-0 pl-2 text-zinc-600">{formatTime(j.created_at)}</span>
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}
