import { useState } from 'react'
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { MoreHorizontal, Plus, Search } from 'lucide-react'
import { api, type AdminUser } from '../../lib/api'
import {
  Button,
  Card,
  ConfirmDialog,
  Dialog,
  Drawer,
  EmptyState,
  ErrorState,
  Field,
  IconButton,
  Input,
  Menu,
  PageHeader,
  Skeleton,
} from '../../ui'
import { errorText } from '../../lib/errorText'
import { formatNumber } from '../../lib/format'
import { useDebouncedValue } from '../../hooks/useDebouncedValue'
import { useToast } from '../../components/Toast'

type Action = 'admin' | 'active' | 'beta'
export default function UsersPage() {
  const { t, i18n } = useTranslation('adminV2')
  const qc = useQueryClient()
  const toast = useToast()
  const [search, setSearch] = useState('')
  const q = useDebouncedValue(search, 300)
  const users = useInfiniteQuery({
    queryKey: ['admin', 'users', q],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => api.adminListUsers({ q, cursor: pageParam, limit: 25 }),
    getNextPageParam: (p) => p.next_cursor || undefined,
  })
  const [grant, setGrant] = useState<AdminUser | null>(null)
  const [create, setCreate] = useState(false)
  const [confirm, setConfirm] = useState<{ user: AdminUser; action: Action } | null>(null)
  const account = (u: AdminUser) => u.email || u.phone || u.biz_id
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['admin'] })
    qc.invalidateQueries({ queryKey: ['me'] })
  }
  const change = useMutation({
    mutationFn: async ({ user: u, action: a }: { user: AdminUser; action: Action }) => {
      if (a === 'admin') await api.adminSetAdmin(u.biz_id, !u.is_admin)
      else if (a === 'active') await api.adminSetActive(u.biz_id, !u.is_active)
      else await api.adminSetComicAI(u.biz_id, !u.comic_ai_enabled)
    },
    onSuccess: () => {
      refresh()
      setConfirm(null)
      toast(t('users.updated'))
    },
  })
  const label = (u: AdminUser, a: Action) =>
    t(
      `users.${a === 'admin' ? (u.is_admin ? 'revokeAdmin' : 'makeAdmin') : a === 'active' ? (u.is_active ? 'suspend' : 'restore') : u.comic_ai_enabled ? 'betaOff' : 'betaOn'}`,
    )
  const num = (n: number) => formatNumber(n, i18n.language)
  const rows = users.data?.pages.flatMap((p) => p.users) ?? []
  const menu = (u: AdminUser) => (
    <Menu
      trigger={<IconButton label={t('users.actions', { account: account(u) })} icon={<MoreHorizontal className="size-5" />} />}
      items={[
        { key: 'grant', label: t('users.grant'), onSelect: () => setGrant(u) },
        ...(['admin', 'active', ...(!u.is_admin ? ['beta'] : [])] as Action[]).map((a) => ({
          key: a,
          label: label(u, a),
          danger: (a === 'active' && u.is_active) || (a === 'admin' && u.is_admin),
          onSelect: () => {
            change.reset()
            setConfirm({ user: u, action: a })
          },
        })),
      ]}
    />
  )
  const values = (u: AdminUser) => [
    num(u.balance),
    num(u.held),
    num(u.credits_spent),
    new Intl.NumberFormat(i18n.language, { style: 'currency', currency: 'CNY' }).format(u.cost_yuan),
    num(u.job_count),
    t(u.is_admin ? 'users.admin' : 'users.member'),
    t(u.is_active ? 'users.active' : 'users.suspended'),
    t(u.is_admin ? 'users.betaAdmin' : u.comic_ai_enabled ? 'users.enabled' : 'users.disabled'),
  ]
  const columns = ['balance', 'held', 'spent', 'cost', 'jobs', 'role', 'status', 'beta']
  return (
    <div className="mx-auto flex min-w-0 max-w-[1600px] flex-col gap-5 px-4 py-6 lg:px-6">
      <PageHeader
        title={t('users.title')}
        description={t('users.description')}
        actions={
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setCreate(true)}>
            {t('users.create')}
          </Button>
        }
      />
      <div className="relative max-w-md">
        <Search aria-hidden className="absolute top-3 left-3 size-4 text-fg-muted" />
        <Input
          aria-label={t('users.search')}
          placeholder={t('users.search')}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="pl-9"
        />
      </div>
      {users.isPending ? (
        <Skeleton className="h-64" />
      ) : users.isError && !rows.length ? (
        <ErrorState message={errorText(t, users.error)} onRetry={() => users.refetch()} />
      ) : !rows.length ? (
        <EmptyState title={t('users.empty')} />
      ) : (
        <>
          <div className="hidden min-w-0 max-w-full overflow-x-auto rounded-card border border-border bg-surface xl:block">
            <table className="w-full whitespace-nowrap text-left text-body">
              <caption className="sr-only">{t('users.title')}</caption>
              <thead className="text-caption text-fg-muted">
                <tr>
                  <th scope="col" className="px-4 py-3">
                    {t('users.account')}
                  </th>
                  {columns.map((c) => (
                    <th scope="col" key={c} className="px-3 py-3">
                      {t(`users.${c}`)}
                    </th>
                  ))}
                  <th scope="col">
                    <span className="sr-only">{t('users.operations')}</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {rows.map((u) => (
                  <tr key={u.biz_id} className="border-t border-border">
                    <th scope="row" className="px-4 py-3 font-medium">
                      {account(u)}
                    </th>
                    {values(u).map((v, i) => (
                      <td key={i} className="px-3 py-3 tabular-nums">
                        {v}
                      </td>
                    ))}
                    <td className="px-2">{menu(u)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <ul className="grid gap-3 xl:hidden">
            {rows.map((u) => (
              <li key={u.biz_id}>
                <Card>
                  <div className="mb-3 flex items-center justify-between gap-3">
                    <h2 className="min-w-0 break-all font-semibold">{account(u)}</h2>
                    {menu(u)}
                  </div>
                  <dl className="grid grid-cols-2 gap-3">
                    {values(u).map((v, i) => (
                      <div key={i}>
                        <dt className="text-caption text-fg-muted">{t(`users.${columns[i]}`)}</dt>
                        <dd className="text-body tabular-nums">{v}</dd>
                      </div>
                    ))}
                  </dl>
                </Card>
              </li>
            ))}
          </ul>
          {users.isFetchNextPageError && <ErrorState message={errorText(t, users.error)} onRetry={() => users.fetchNextPage()} />}
          {users.hasNextPage && (
            <Button loading={users.isFetchingNextPage} onClick={() => users.fetchNextPage()}>
              {t('users.more')}
            </Button>
          )}
        </>
      )}
      <ConfirmDialog
        open={!!confirm}
        onOpenChange={(o) => !o && setConfirm(null)}
        title={confirm ? label(confirm.user, confirm.action) : ''}
        target={confirm ? account(confirm.user) : ''}
        effects={
          confirm
            ? [
                t(
                  `effects.${confirm.action}.${confirm.action === 'admin' ? String(!confirm.user.is_admin) : confirm.action === 'active' ? String(!confirm.user.is_active) : String(!confirm.user.comic_ai_enabled)}`,
                ),
              ]
            : []
        }
        body={
          change.isError ? (
            <p role="alert" className="text-danger-fg">
              {errorText(t, change.error)}
            </p>
          ) : undefined
        }
        confirmLabel={t('users.confirm')}
        busy={change.isPending}
        danger={confirm?.action === 'active' && confirm.user.is_active}
        onConfirm={() => confirm && change.mutate(confirm)}
      />
      {grant && <Grant user={grant} onClose={() => setGrant(null)} onSaved={refresh} />}
      {create && <CreateUser onClose={() => setCreate(false)} onSaved={refresh} />}
    </div>
  )
}
function Grant({ user, onClose, onSaved }: { user: AdminUser; onClose: () => void; onSaved: () => void }) {
  const { t, i18n } = useTranslation('adminV2')
  const [amount, setAmount] = useState('200')
  const [remark, setRemark] = useState('')
  const [tried, setTried] = useState(false)
  const n = Number(amount)
  const valid = Number.isSafeInteger(n) && n > 0 && n <= 1000000
  const save = useMutation({
    mutationFn: () => api.adminGrantCredits(user.biz_id, n, remark.trim() || undefined),
    onSuccess: () => {
      onSaved()
      onClose()
    },
  })
  return (
    <Drawer
      open
      onOpenChange={(o) => !o && !save.isPending && onClose()}
      title={t('users.grant')}
      description={user.email || user.phone || user.biz_id}
      footer={
        <Button
          variant="primary"
          loading={save.isPending}
          onClick={() => {
            if (save.isPending) return
            setTried(true)
            if (valid) save.mutate()
          }}
        >
          {t('users.grant')}
        </Button>
      }
    >
      <div className="space-y-4">
        <Field label={t('users.amount')} required error={tried && !valid ? t('users.amountError') : undefined}>
          <Input inputMode="numeric" value={amount} onChange={(e) => setAmount(e.target.value)} />
        </Field>
        <Field label={t('users.reason')} optional>
          <Input value={remark} onChange={(e) => setRemark(e.target.value)} />
        </Field>
        <p className="text-body">{t('users.after', { n: formatNumber(user.balance + (valid ? n : 0), i18n.language) })}</p>
        {save.isError && (
          <p role="alert" className="text-danger-fg">
            {errorText(t, save.error)}
          </p>
        )}
      </div>
    </Drawer>
  )
}
function CreateUser({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation('adminV2')
  const [email, setEmail] = useState('')
  const [tried, setTried] = useState(false)
  const [copied, setCopied] = useState(false)
  const [copyFailed, setCopyFailed] = useState(false)
  const valid = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())
  const save = useMutation({ mutationFn: () => api.adminCreateUser(email.trim()), onSuccess: onSaved })
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      locked={save.isPending}
      title={t('users.create')}
      footer={
        save.data ? (
          <Button onClick={onClose}>{t('users.done')}</Button>
        ) : (
          <Button
            variant="primary"
            loading={save.isPending}
            onClick={() => {
              if (save.isPending) return
              setTried(true)
              if (valid) save.mutate()
            }}
          >
            {t('users.create')}
          </Button>
        )
      }
    >
      {save.data ? (
        <div className="space-y-3">
          <p>{save.data.email}</p>
          <p className="text-body text-warning-fg">{t('users.once')}</p>
          <Field label={t('users.tempPassword')}>
            <Input readOnly value={save.data.temp_password} />
          </Field>
          <Button
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(save.data!.temp_password)
                setCopied(true)
                setCopyFailed(false)
              } catch {
                setCopyFailed(true)
              }
            }}
          >
            {t(copied ? 'users.copied' : 'users.copy')}
          </Button>
          {copyFailed && <p role="alert">{t('users.copyFailed')}</p>}
        </div>
      ) : (
        <Field label={t('users.email')} required error={tried && !valid ? t('users.emailError') : undefined}>
          <Input type="email" autoComplete="off" value={email} onChange={(e) => setEmail(e.target.value)} />
        </Field>
      )}
      {save.isError && (
        <p role="alert" className="mt-3 text-danger-fg">
          {errorText(t, save.error)}
        </p>
      )}
    </Dialog>
  )
}
