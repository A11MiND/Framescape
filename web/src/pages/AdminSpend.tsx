import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import AdminShell from '../components/AdminShell'

// Money/usage half of the admin dashboard, split from AdminUsers.tsx (人员
// 管理) — an admin checking today's spend has no reason to load or scroll
// past the user table, and vice versa. Reachable only when GET /me's
// is_admin is true (App.tsx's RequireAdmin); every request here still
// 403s server-side regardless (requireAdmin, server.go), so this page is
// defense-in-depth on top of a real backend boundary, not the boundary
// itself.
export default function AdminSpend() {
  const { t, i18n } = useTranslation()

  const overview = useQuery({ queryKey: ['admin', 'overview'], queryFn: api.adminOverview })
  const usage = useQuery({ queryKey: ['admin', 'usage'], queryFn: () => api.adminUsage(30) })

  const jobsByStatus = overview.data?.jobs_by_status ?? {}
  const totalJobs = Object.values(jobsByStatus).reduce((a, b) => a + b, 0)
  const dateFmt = (d: string) =>
    new Date(d).toLocaleDateString(i18n.language === 'en' ? 'en-US' : 'zh-CN', { month: '2-digit', day: '2-digit' })
  const yuanFmt = (v: number) => `¥${v.toFixed(2)}`

  return (
    <AdminShell>
      <div className="space-y-8">
        {/* Overview stat tiles */}
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <StatTile label={t('admin.totalJobs')} value={totalJobs} />
          <StatTile label={t('admin.creditsRecharged')} value={overview.data?.credits_recharged ?? 0} />
          <StatTile label={t('admin.creditsConsumed')} value={overview.data?.credits_consumed ?? 0} />
          {/* Real ¥ MiniMax has actually charged this account — see
              AdminOverview.total_cost_yuan's own doc in lib/api.ts for why
              this isn't just the credits figure in a different unit. */}
          <StatTile label={t('admin.totalCostYuan')} value={yuanFmt(overview.data?.total_cost_yuan ?? 0)} accent />
        </div>

        {/* Jobs by status */}
        <div className="rounded-xl border border-zinc-800 bg-zinc-900/60 p-5">
          <p className="mb-3 text-xs uppercase tracking-wide text-zinc-500">{t('admin.jobsByStatus')}</p>
          <div className="flex flex-wrap gap-4">
            {Object.entries(jobsByStatus).map(([status, n]) => (
              <div key={status} className="flex items-baseline gap-1.5">
                <span className="font-mono text-lg text-zinc-200">{n}</span>
                <span className="text-xs text-zinc-500">{t(`admin.status.${status}`, status)}</span>
              </div>
            ))}
            {overview.isSuccess && totalJobs === 0 && <p className="text-sm text-zinc-500">{t('admin.noJobs')}</p>}
          </div>
        </div>

        {/* Usage over time */}
        <div>
          <p className="mb-3 text-xs uppercase tracking-wide text-zinc-500">{t('admin.usageLast30Days')}</p>
          <div className="overflow-x-auto rounded-xl border border-zinc-800">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-zinc-800 text-left text-xs uppercase tracking-wide text-zinc-500">
                  <th className="px-4 py-2 font-normal">{t('admin.day')}</th>
                  <th className="px-4 py-2 text-right font-normal">{t('admin.jobsCol')}</th>
                  <th className="px-4 py-2 text-right font-normal">{t('admin.creditsConsumedCol')}</th>
                  <th className="px-4 py-2 text-right font-normal">{t('admin.costYuanCol')}</th>
                </tr>
              </thead>
              <tbody>
                {(usage.data?.days ?? []).map((d) => (
                  <tr key={d.day} className="border-b border-zinc-800 last:border-0">
                    <td className="px-4 py-2 text-zinc-400">{dateFmt(d.day)}</td>
                    <td className="px-4 py-2 text-right font-mono text-zinc-300">{d.jobs}</td>
                    <td className="px-4 py-2 text-right font-mono text-zinc-300">{d.credits_consumed}</td>
                    <td className="px-4 py-2 text-right font-mono text-emerald-400">{yuanFmt(d.cost_yuan)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {usage.isSuccess && (usage.data?.days.length ?? 0) === 0 && (
              <p className="p-4 text-center text-sm text-zinc-500">{t('admin.noUsage')}</p>
            )}
          </div>
        </div>
      </div>
    </AdminShell>
  )
}

function StatTile({ label, value, accent }: { label: string; value: number | string; accent?: boolean }) {
  return (
    <div className="rounded-xl border border-zinc-800 bg-zinc-900/60 p-5">
      <p className="text-xs uppercase tracking-wide text-zinc-500">{label}</p>
      <p className={`mt-1 font-mono text-2xl ${accent ? 'text-emerald-400' : 'text-zinc-100'}`}>{value}</p>
    </div>
  )
}
