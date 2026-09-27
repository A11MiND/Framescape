import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RotateCcw, Trash2 } from 'lucide-react'
import { Button, ConfirmDialog, EmptyState, ErrorState, Skeleton } from '../../ui'
import { assetsApi } from '../../lib/api/assets'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { useToast } from '../../components/Toast'
import { AssetCard } from './AssetCard'

/** Deleted assets: restore one, or empty the trash for good (stating how many). */
export function TrashView({ retention }: { retention: number }) {
  const { t } = useTranslation('library')
  const qc = useQueryClient()
  const toast = useToast()
  const trash = useQuery({ queryKey: [...keys.assets.all, 'trash'], queryFn: assetsApi.trash })
  const [confirming, setConfirming] = useState(false)
  const refresh = () => qc.invalidateQueries({ queryKey: keys.assets.all })
  const restore = useMutation({
    mutationFn: (id: string) => assetsApi.batch('restore', [id]),
    onSuccess: (res) => {
      toast(t('done.restored', { n: res.affected, count: res.affected }))
      refresh()
    },
    onError: (err) => toast(errorText(t, err)),
  })
  const empty = useMutation({
    mutationFn: assetsApi.emptyTrash,
    onSuccess: (res) => {
      setConfirming(false)
      toast(t('trash.emptied', { n: res.purged, count: res.purged }))
      refresh()
    },
    onError: (err) => toast(errorText(t, err)),
  })

  if (trash.isPending) return <Skeleton className="h-48 w-full" />
  if (trash.isError) return <ErrorState message={errorText(t, trash.error)} onRetry={() => trash.refetch()} />
  const total = trash.data.total
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-caption text-fg-muted">{t('trash.hint', { days: retention })}</p>
        {total > 0 && (
          <Button variant="danger-outline" icon={<Trash2 aria-hidden className="size-4" />} onClick={() => setConfirming(true)}>
            {t('trash.emptyAll', { n: total })}
          </Button>
        )}
      </div>
      {total === 0 ? (
        <EmptyState icon={<Trash2 className="size-7" />} title={t('trash.empty')} />
      ) : (
        <ul className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3 sm:grid-cols-[repeat(auto-fill,minmax(220px,1fr))]">
          {trash.data.assets.map((a) => (
            <AssetCard
              key={a.biz_id}
              asset={a}
              selecting={false}
              selected={false}
              onToggle={() => {}}
              footer={
                <div className="mt-2 flex items-center justify-between gap-2">
                  <span className="text-caption text-warning-fg">{t('trash.daysLeft', { n: a.days_until_purge })}</span>
                  <Button size="sm" icon={<RotateCcw aria-hidden className="size-4" />} loading={restore.isPending && restore.variables === a.biz_id} onClick={() => restore.mutate(a.biz_id)}>
                    {t('trash.restore')}
                  </Button>
                </div>
              }
            />
          ))}
        </ul>
      )}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('trash.emptyTitle')}
        body={t('trash.emptyBody', { n: total, count: total })}
        confirmLabel={t('trash.emptyConfirm')}
        danger
        busy={empty.isPending}
        onConfirm={() => empty.mutate()}
      />
    </div>
  )
}
