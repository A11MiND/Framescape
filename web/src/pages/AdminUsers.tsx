import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, ApiError, type AdminUser } from '../lib/api'
import AdminShell from '../components/AdminShell'
import { useToast } from '../components/Toast'
import { useDebouncedValue } from '../hooks/useDebouncedValue'

// Account-management half of the admin dashboard, split from AdminSpend.tsx
// (花费) — see that file's own doc for why. Reachable only when GET /me's
// is_admin is true (App.tsx's RequireAdmin); every request here still
// 403s server-side regardless (requireAdmin, server.go), so this page is
// defense-in-depth on top of a real backend boundary, not the boundary
// itself.
export default function AdminUsers() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const pushToast = useToast()
  const [search, setSearch] = useState('')
  const debouncedSearch = useDebouncedValue(search, 300)
  const [grantTarget, setGrantTarget] = useState<AdminUser | null>(null)
  const [showCreateUser, setShowCreateUser] = useState(false)

  const overview = useQuery({ queryKey: ['admin', 'overview'], queryFn: api.adminOverview })
  const users = useQuery({
    queryKey: ['admin', 'users', debouncedSearch || undefined],
    queryFn: () => api.adminListUsers({ q: debouncedSearch || undefined, limit: 100 }),
  })

  const setAdmin = useMutation({
    mutationFn: ({ bizId, isAdmin }: { bizId: string; isAdmin: boolean }) => api.adminSetAdmin(bizId, isAdmin),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'users'] }),
    onError: (err) => pushToast(err instanceof ApiError ? err.message : t('admin.actionFailed')),
  })
  const setActive = useMutation({
    mutationFn: ({ bizId, isActive }: { bizId: string; isActive: boolean }) => api.adminSetActive(bizId, isActive),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'users'] }),
    onError: (err) => pushToast(err instanceof ApiError ? err.message : t('admin.actionFailed')),
  })

  const yuanFmt = (v: number) => `¥${v.toFixed(2)}`

  return (
    <AdminShell>
      <div className="space-y-8">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <StatTile label={t('admin.users')} value={overview.data?.user_count ?? 0} />
        </div>

        {/* User list */}
        <div>
          <div className="mb-3 flex items-center justify-between gap-3">
            <p className="text-xs uppercase tracking-wide text-zinc-500">{t('admin.usersTitle')}</p>
            <div className="flex items-center gap-2">
              <input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t('admin.searchPlaceholder')}
                className="w-56 rounded-lg border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-200 placeholder:text-zinc-600 focus:border-violet-500 focus:outline-none"
              />
              <button
                onClick={() => setShowCreateUser(true)}
                className="rounded-lg bg-violet-500 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-violet-400"
              >
                {t('admin.newUser')}
              </button>
            </div>
          </div>
          <div className="overflow-x-auto rounded-xl border border-zinc-800">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-zinc-800 text-left text-xs uppercase tracking-wide text-zinc-500">
                  <th className="px-4 py-2 font-normal">{t('admin.colAccount')}</th>
                  <th className="px-4 py-2 text-right font-normal">{t('admin.colBalance')}</th>
                  <th className="px-4 py-2 text-right font-normal">{t('admin.colHeld')}</th>
                  <th className="px-4 py-2 text-right font-normal">{t('admin.colSpent')}</th>
                  <th className="px-4 py-2 text-right font-normal">{t('admin.colCostYuan')}</th>
                  <th className="px-4 py-2 text-right font-normal">{t('admin.colJobs')}</th>
                  <th className="px-4 py-2 font-normal">{t('admin.colRole')}</th>
                  <th className="px-4 py-2 font-normal">{t('admin.colStatus')}</th>
                  <th className="px-4 py-2 font-normal"></th>
                </tr>
              </thead>
              <tbody>
                {(users.data?.users ?? []).map((u) => (
                  <tr key={u.biz_id} className={`border-b border-zinc-800 last:border-0 ${u.is_active ? '' : 'opacity-50'}`}>
                    <td className="px-4 py-2.5 text-zinc-300">{u.email ?? u.phone ?? u.biz_id}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-violet-300">{u.balance}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-zinc-400">{u.held}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-zinc-400">{u.credits_spent}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-emerald-400">{yuanFmt(u.cost_yuan)}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-zinc-400">{u.job_count}</td>
                    <td className="px-4 py-2.5">
                      {u.is_admin ? (
                        <span className="rounded-full bg-violet-500/20 px-2 py-0.5 text-xs text-violet-300">
                          {t('admin.roleAdmin')}
                        </span>
                      ) : (
                        <span className="text-xs text-zinc-600">{t('admin.roleUser')}</span>
                      )}
                    </td>
                    <td className="px-4 py-2.5">
                      {u.is_active ? (
                        <span className="text-xs text-emerald-400">{t('admin.statusActive')}</span>
                      ) : (
                        <span className="rounded-full bg-red-500/20 px-2 py-0.5 text-xs text-red-300">
                          {t('admin.statusSuspended')}
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <button
                          onClick={() => setGrantTarget(u)}
                          className="rounded-lg border border-zinc-800 px-2.5 py-1 text-xs text-zinc-300 transition hover:border-violet-500 hover:text-violet-300"
                        >
                          {t('admin.grantCredits')}
                        </button>
                        <button
                          onClick={() => setAdmin.mutate({ bizId: u.biz_id, isAdmin: !u.is_admin })}
                          disabled={setAdmin.isPending}
                          className="rounded-lg border border-zinc-800 px-2.5 py-1 text-xs text-zinc-300 transition hover:border-zinc-600 disabled:opacity-50"
                        >
                          {u.is_admin ? t('admin.revokeAdmin') : t('admin.makeAdmin')}
                        </button>
                        <button
                          onClick={() => setActive.mutate({ bizId: u.biz_id, isActive: !u.is_active })}
                          disabled={setActive.isPending}
                          className={`rounded-lg border px-2.5 py-1 text-xs transition disabled:opacity-50 ${
                            u.is_active
                              ? 'border-zinc-800 text-zinc-300 hover:border-red-500 hover:text-red-300'
                              : 'border-zinc-800 text-zinc-300 hover:border-emerald-500 hover:text-emerald-300'
                          }`}
                        >
                          {u.is_active ? t('admin.suspend') : t('admin.reactivate')}
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {users.isSuccess && (users.data?.users.length ?? 0) === 0 && (
              <p className="p-4 text-center text-sm text-zinc-500">{t('admin.noUsers')}</p>
            )}
          </div>
        </div>
      </div>

      {grantTarget && <GrantCreditsDialog user={grantTarget} onClose={() => setGrantTarget(null)} />}
      {showCreateUser && <CreateUserDialog onClose={() => setShowCreateUser(false)} />}
    </AdminShell>
  )
}

function StatTile({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-xl border border-zinc-800 bg-zinc-900/60 p-5">
      <p className="text-xs uppercase tracking-wide text-zinc-500">{label}</p>
      <p className="mt-1 font-mono text-2xl text-zinc-100">{value}</p>
    </div>
  )
}

function GrantCreditsDialog({ user, onClose }: { user: AdminUser; onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const pushToast = useToast()
  const [amount, setAmount] = useState('200')
  const [remark, setRemark] = useState('')

  const grant = useMutation({
    mutationFn: () => api.adminGrantCredits(user.biz_id, Number(amount), remark || undefined),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'users'] })
      qc.invalidateQueries({ queryKey: ['admin', 'overview'] })
      pushToast(t('admin.grantSucceeded', { amount, account: user.email ?? user.phone ?? user.biz_id }))
      onClose()
    },
    onError: (err) => pushToast(err instanceof ApiError ? err.message : t('admin.actionFailed')),
  })

  const amountNum = Number(amount)
  const validAmount = Number.isInteger(amountNum) && amountNum > 0 && amountNum <= 1_000_000

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4" onClick={onClose}>
      <div
        className="w-full max-w-sm rounded-xl border border-zinc-800 bg-zinc-900 p-5"
        onClick={(e) => e.stopPropagation()}
      >
        <p className="mb-1 text-sm font-medium text-zinc-200">{t('admin.grantCreditsTitle')}</p>
        <p className="mb-4 text-xs text-zinc-500">{user.email ?? user.phone ?? user.biz_id}</p>

        <label className="mb-3 block">
          <span className="mb-1 block text-xs text-zinc-500">{t('admin.amount')}</span>
          <input
            type="number"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            min={1}
            max={1_000_000}
            className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-1.5 text-sm text-zinc-200 focus:border-violet-500 focus:outline-none"
          />
        </label>
        <label className="mb-4 block">
          <span className="mb-1 block text-xs text-zinc-500">{t('admin.remarkOptional')}</span>
          <input
            value={remark}
            onChange={(e) => setRemark(e.target.value)}
            placeholder={t('admin.remarkPlaceholder')}
            className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-1.5 text-sm text-zinc-200 placeholder:text-zinc-600 focus:border-violet-500 focus:outline-none"
          />
        </label>

        <div className="flex justify-end gap-2">
          <button
            onClick={onClose}
            className="rounded-lg border border-zinc-800 px-3 py-1.5 text-sm text-zinc-400 transition hover:text-zinc-200"
          >
            {t('common.cancel')}
          </button>
          <button
            onClick={() => grant.mutate()}
            disabled={!validAmount || grant.isPending}
            className="rounded-lg bg-violet-500 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
          >
            {grant.isPending ? t('admin.granting') : t('admin.confirmGrant')}
          </button>
        </div>
      </div>
    </div>
  )
}

// CreateUserDialog is the "provision an account for a colleague" flow this
// section's own doc in api.ts covers — a two-phase dialog: enter an email,
// then the server-generated temp password is shown exactly once (never
// retrievable again, never logged) for the admin to copy and relay through
// whatever channel they choose.
function CreateUserDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const pushToast = useToast()
  const [email, setEmail] = useState('')
  const [result, setResult] = useState<{ email: string; tempPassword: string } | null>(null)

  const create = useMutation({
    mutationFn: () => api.adminCreateUser(email.trim()),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ['admin', 'users'] })
      qc.invalidateQueries({ queryKey: ['admin', 'overview'] })
      setResult({ email: res.email, tempPassword: res.temp_password })
    },
    onError: (err) => pushToast(err instanceof ApiError ? err.message : t('admin.actionFailed')),
  })

  const copyPassword = async () => {
    if (!result) return
    try {
      await navigator.clipboard.writeText(result.tempPassword)
      pushToast(t('admin.copied'))
    } catch {
      // Clipboard permission denied or unavailable — the password is still
      // visible on screen to select/copy by hand, so this isn't fatal.
      pushToast(t('admin.copyFailed'))
    }
  }

  const validEmail = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4" onClick={onClose}>
      <div
        className="w-full max-w-sm rounded-xl border border-zinc-800 bg-zinc-900 p-5"
        onClick={(e) => e.stopPropagation()}
      >
        {!result ? (
          <>
            <p className="mb-4 text-sm font-medium text-zinc-200">{t('admin.newUserTitle')}</p>
            <label className="mb-4 block">
              <span className="mb-1 block text-xs text-zinc-500">{t('admin.newUserEmailLabel')}</span>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="teammate@example.com"
                autoFocus
                className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-1.5 text-sm text-zinc-200 placeholder:text-zinc-600 focus:border-violet-500 focus:outline-none"
              />
            </label>
            <div className="flex justify-end gap-2">
              <button
                onClick={onClose}
                className="rounded-lg border border-zinc-800 px-3 py-1.5 text-sm text-zinc-400 transition hover:text-zinc-200"
              >
                {t('common.cancel')}
              </button>
              <button
                onClick={() => create.mutate()}
                disabled={!validEmail || create.isPending}
                className="rounded-lg bg-violet-500 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
              >
                {create.isPending ? t('admin.creating') : t('admin.confirmCreate')}
              </button>
            </div>
          </>
        ) : (
          <>
            <p className="mb-1 text-sm font-medium text-zinc-200">{t('admin.newUserCreated')}</p>
            <p className="mb-4 text-xs text-zinc-500">{result.email}</p>
            <div className="mb-2 flex items-center gap-2 rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2">
              <code className="flex-1 select-all font-mono text-sm text-emerald-400">{result.tempPassword}</code>
              <button
                onClick={copyPassword}
                className="shrink-0 rounded-md border border-zinc-800 px-2 py-1 text-xs text-zinc-300 transition hover:border-violet-500 hover:text-violet-300"
              >
                {t('admin.copy')}
              </button>
            </div>
            <p className="mb-4 text-xs text-amber-400">{t('admin.newUserPasswordWarning')}</p>
            <div className="flex justify-end">
              <button
                onClick={onClose}
                className="rounded-lg bg-violet-500 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-violet-400"
              >
                {t('admin.done')}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
