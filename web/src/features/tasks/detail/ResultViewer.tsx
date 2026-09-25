import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useMutation, useQueries, useQueryClient } from '@tanstack/react-query'
import { Check, ChevronLeft, ChevronRight, Clapperboard, Download, FolderOpen, Globe, UserRound } from 'lucide-react'
import { Button, ConfirmDialog, IconButton, Skeleton, buttonClasses, cn } from '../../../ui'
import { assetsApi, type AssetInfo } from '../../../lib/api/assets'
import { keys } from '../../../lib/api/keys'
import { errorText } from '../../../lib/errorText'
import { useToast } from '../../../components/Toast'

function gridColumns(n: number) {
  return n === 2 || n === 4 ? 'grid-cols-2' : 'grid-cols-2 sm:grid-cols-3'
}

/**
 * The results of a job and actions by asset type for the selected one.
 * "viewer" shows one large contained view with a strip to switch; "grid" lays
 * several results out by count (2 and 4 in two columns, more in three).
 */
export function ResultViewer({ assetIds, showTitle = true, layout = 'viewer' }: { assetIds: string[]; showTitle?: boolean; layout?: 'viewer' | 'grid' }) {
  const { t } = useTranslation('job')
  const navigate = useNavigate()
  const qc = useQueryClient()
  const toast = useToast()
  const [current, setCurrent] = useState(0)
  const [confirmPublish, setConfirmPublish] = useState(false)
  const assets = useQueries({
    queries: assetIds.map((id) => ({ queryKey: keys.assets.detail(id), queryFn: () => assetsApi.get(id) })),
  })
  const index = Math.min(current, assetIds.length - 1)
  const active = assets[index]
  const asset: AssetInfo | undefined = active?.data

  const publish = useMutation({
    mutationFn: (a: AssetInfo) => assetsApi.setPublic(a.biz_id, !a.is_public),
    onSuccess: (_, a) => {
      setConfirmPublish(false)
      toast(a.is_public ? t('results.unpublished') : t('results.published'))
      qc.invalidateQueries({ queryKey: keys.assets.detail(a.biz_id) })
    },
    onError: (err) => toast(errorText(t, err)),
  })

  return (
    <section aria-labelledby="results-title" className="flex flex-col gap-3">
      <div className="flex items-baseline justify-between gap-3">
        <h2 id="results-title" className={showTitle ? 'text-section font-semibold text-fg' : 'sr-only'}>
          {t('results.title')}
        </h2>
        {assetIds.length > 1 && <span className="text-caption text-fg-muted">{t('results.count', { n: assetIds.length })}</span>}
      </div>

      {layout === 'grid' && assetIds.length > 1 ? (
        <ul className={cn('grid gap-2', gridColumns(assetIds.length))}>
          {assets.map((q, i) => (
            <li key={assetIds[i]}>
              <button
                type="button"
                aria-label={t('results.pick', { n: i + 1 })}
                aria-pressed={i === index}
                onClick={() => setCurrent(i)}
                className={cn(
                  'relative block aspect-[3/2] w-full overflow-hidden rounded-card border-2 bg-surface-2',
                  i === index ? 'border-primary' : 'border-transparent hover:border-border-control',
                )}
              >
                {q.isError ? (
                  <span className="text-caption text-fg-muted">{t('results.missing')}</span>
                ) : q.data ? (
                  <img src={q.data.thumb_url || q.data.public_url} alt="" className="size-full object-cover" />
                ) : (
                  <Skeleton className="size-full" />
                )}
                {i === index && (
                  <span className="absolute top-2 left-2 inline-flex size-6 items-center justify-center rounded-full bg-primary text-white">
                    <Check aria-hidden className="size-4" />
                  </span>
                )}
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <>
        <div className="relative flex aspect-[3/2] w-full items-center justify-center overflow-hidden rounded-card border border-border bg-surface-2">
          {active?.isError ? (
            <p className="text-body text-fg-muted">{t('results.missing')}</p>
          ) : !asset ? (
            <Skeleton className="size-full" />
          ) : asset.type === 'video' ? (
            <video src={asset.public_url} poster={asset.thumb_url || undefined} controls className="size-full bg-black object-contain" />
          ) : (
            <img src={asset.public_url} alt="" className="size-full object-contain" />
          )}
          {asset?.resolution_tag && (
            <span className="absolute top-3 left-3 rounded-badge bg-black/70 px-2 py-0.5 text-badge font-medium text-white">{asset.resolution_tag}</span>
          )}
          {assetIds.length > 1 && (
            <>
              <IconButton
                label={t('results.prev')}
                icon={<ChevronLeft className="size-5" />}
                onClick={() => setCurrent((index - 1 + assetIds.length) % assetIds.length)}
                className="absolute top-1/2 left-3 -translate-y-1/2 rounded-full bg-black/55 text-white hover:bg-black/70 hover:text-white"
              />
              <IconButton
                label={t('results.next')}
                icon={<ChevronRight className="size-5" />}
                onClick={() => setCurrent((index + 1) % assetIds.length)}
                className="absolute top-1/2 right-3 -translate-y-1/2 rounded-full bg-black/55 text-white hover:bg-black/70 hover:text-white"
              />
            </>
          )}
        </div>

        {assetIds.length > 1 && (
          <ul className="flex gap-2 overflow-x-auto pb-1">
            {assets.map((q, i) => (
              <li key={assetIds[i]}>
                <button
                  type="button"
                  aria-label={t('results.pick', { n: i + 1 })}
                  aria-current={i === index || undefined}
                  onClick={() => setCurrent(i)}
                  className={cn('block size-20 overflow-hidden rounded-thumb border-2 bg-surface-2', i === index ? 'border-primary' : 'border-transparent')}
                >
                  {q.data && <img src={q.data.thumb_url || q.data.public_url} alt="" className="size-full object-cover" />}
                </button>
              </li>
            ))}
          </ul>
        )}
        </>
      )}

      {asset && (
        <div className="flex flex-wrap gap-2">
          <a href={asset.public_url} target="_blank" rel="noreferrer" download className={buttonClasses('primary', 'md')}>
            <Download aria-hidden className="size-4" />
            {t('action.download')}
          </a>
          <Link to={`/assets/${asset.biz_id}`} className={buttonClasses('secondary', 'md')}>
            <FolderOpen aria-hidden className="size-4" />
            {t('action.openAsset')}
          </Link>
          {asset.type === 'image' && (
            <Button
              icon={<Clapperboard aria-hidden className="size-4" />}
              onClick={() => navigate('/', { state: { prefillSuggestion: { kind: 'to-video', label: '', sourceAssetId: asset.biz_id } } })}
            >
              {t('action.toVideo')}
            </Button>
          )}
          {asset.type === 'image' && (
            <Button icon={<UserRound aria-hidden className="size-4" />} onClick={() => navigate('/characters', { state: { prefillAssetId: asset.biz_id } })}>
              {t('action.saveCharacter')}
            </Button>
          )}
          <Button
            icon={<Globe aria-hidden className="size-4" />}
            loading={publish.isPending && !confirmPublish}
            onClick={() => (asset.is_public ? publish.mutate(asset) : setConfirmPublish(true))}
          >
            {asset.is_public ? t('action.unpublish') : t('action.publish')}
          </Button>
        </div>
      )}
      {asset && (
        <ConfirmDialog
          open={confirmPublish}
          onOpenChange={setConfirmPublish}
          title={t('publish.title')}
          effects={[t('publish.effect1'), t('publish.effect2')]}
          confirmLabel={t('publish.confirm')}
          busy={publish.isPending}
          onConfirm={() => publish.mutate(asset)}
        />
      )}
    </section>
  )
}
