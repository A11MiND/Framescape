import { useParams, useNavigate, Link } from 'react-router-dom'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import AppShell from '../components/AppShell'
import { useToast } from '../components/Toast'

const META_LABEL_KEY: Record<string, string> = {
  model: 'assetDetail.meta.model',
  prompt: 'assetDetail.meta.prompt',
  seed: 'assetDetail.meta.seed',
  mode: 'assetDetail.meta.mode',
  layout: 'assetDetail.meta.layout',
  minimax_task_id: 'assetDetail.meta.providerTaskId',
}

// F2.5's full asset detail: generation params (assets.meta, already written
// by every executor — see assetDetailJSON's own doc), the source job link,
// and "以此再生成" — §19.0②'s "参数公开 + 一键同款" applied to a single
// user's own history rather than a public community feed. The grid at
// /assets only ever had type/dimensions to show; everything here needed
// batch 2's GET /assets/{id} to grow a `meta`/`job_biz_id` field first.
export default function AssetDetail() {
  const { t, i18n } = useTranslation()
  const { assetId } = useParams<{ assetId: string }>()
  const navigate = useNavigate()
  const pushToast = useToast()

  const asset = useQuery({
    queryKey: ['asset-detail', assetId],
    queryFn: () => api.getAsset(assetId!),
    enabled: !!assetId,
  })
  const jobBizId = asset.data?.job_biz_id
  const job = useQuery({
    queryKey: ['job', jobBizId],
    queryFn: () => api.getJob(jobBizId!),
    enabled: !!jobBizId,
  })

  const del = useMutation({
    mutationFn: () => api.deleteAsset(assetId!),
    onSuccess: () => navigate('/assets'),
    onError: () => pushToast(t('assets.deleteFailed'), () => del.mutate()),
  })

  function regenerate() {
    if (!job.data) return
    navigate('/', { state: { prefillJob: { workflowName: job.data.workflow_name, spec: job.data.spec } } })
  }

  const a = asset.data
  const metaEntries = a?.meta ? Object.entries(a.meta).filter(([k]) => k !== 'index') : []

  return (
    <AppShell>
      <div className="mx-auto max-w-3xl px-6 py-8">
        <Link to="/assets" className="mb-4 inline-block text-sm text-zinc-400 hover:text-zinc-200">
          {t('assetDetail.backToLibrary')}
        </Link>

        {!a && <p className="text-zinc-500">{t('common.loading')}</p>}

        {a && (
          <div className="space-y-4">
            <div className="overflow-hidden rounded-xl border border-zinc-800 bg-black">
              {a.type === 'video' ? (
                <video src={a.public_url} controls className="max-h-[70vh] w-full object-contain" />
              ) : (
                <img src={a.public_url} alt="" className="max-h-[70vh] w-full object-contain" />
              )}
            </div>

            <div className="rounded-xl border border-zinc-800 bg-zinc-900/60 p-5">
              <div className="mb-3 flex flex-wrap items-center gap-2">
                {a.resolution_tag && (
                  <span className="rounded-full bg-violet-500/20 px-2.5 py-0.5 text-xs font-medium text-violet-300">
                    {a.resolution_tag}
                  </span>
                )}
                <span className="rounded-full border border-zinc-700 px-2.5 py-0.5 text-xs text-zinc-400">
                  {a.width}×{a.height}
                </span>
                <span className="rounded-full border border-zinc-700 px-2.5 py-0.5 text-xs text-zinc-400">{a.mime}</span>
                <span className="text-xs text-zinc-600">
                  {new Date(a.created_at).toLocaleString(i18n.language === 'en' ? 'en-US' : 'zh-CN', {
                    dateStyle: 'medium',
                    timeStyle: 'short',
                  })}
                </span>
              </div>

              {metaEntries.length > 0 ? (
                <dl className="space-y-1.5 text-sm">
                  {metaEntries.map(([k, v]) => (
                    <div key={k} className="flex gap-2">
                      <dt className="w-20 shrink-0 text-zinc-500">{META_LABEL_KEY[k] ? t(META_LABEL_KEY[k]) : k}</dt>
                      <dd className="min-w-0 flex-1 break-words text-zinc-300">{String(v)}</dd>
                    </div>
                  ))}
                </dl>
              ) : (
                <p className="text-sm text-zinc-600">{t('assetDetail.uploadedNoParams')}</p>
              )}

              {jobBizId && (
                <Link to={`/jobs/${jobBizId}`} className="mt-3 inline-block text-sm text-violet-400 hover:text-violet-300">
                  {t('assetDetail.fromJob', { id: jobBizId.slice(0, 10) })}
                </Link>
              )}
            </div>

            <div className="flex flex-wrap gap-2">
              {job.data && (
                <button
                  onClick={regenerate}
                  className="rounded-lg bg-violet-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-violet-400"
                >
                  ✏️ {t('assetDetail.regenerateFromThis')}
                </button>
              )}
              {a.type === 'image' && (
                <button
                  onClick={() => navigate('/characters', { state: { prefillAssetId: a.biz_id } })}
                  className="rounded-lg border border-zinc-700 px-4 py-2 text-sm text-zinc-200 transition hover:border-zinc-600 hover:bg-zinc-800"
                >
                  {t('assetDetail.saveAsCharacter')}
                </button>
              )}
              <a
                href={a.public_url}
                download
                target="_blank"
                rel="noreferrer"
                className="rounded-lg border border-zinc-700 px-4 py-2 text-sm text-zinc-200 transition hover:border-zinc-600 hover:bg-zinc-800"
              >
                {t('assetDetail.download')}
              </a>
              <button
                onClick={() => del.mutate()}
                disabled={del.isPending}
                className="rounded-lg border border-zinc-700 px-4 py-2 text-sm text-zinc-400 transition hover:border-red-500 hover:text-red-400 disabled:opacity-50"
              >
                {del.isPending ? t('assetDetail.deleting') : t('common.delete')}
              </button>
            </div>
          </div>
        )}
      </div>
    </AppShell>
  )
}
