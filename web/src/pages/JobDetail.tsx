import { useEffect } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { resultAssetIds, resolveTab, statusToPhase, WORKFLOW_LABEL_KEY } from '../lib/jobResult'
import { displayNodeError, firstSpecificError } from '../lib/errors'
import { suggestActions, type SuggestedAction } from '../lib/suggestions'
import { useToast } from '../components/Toast'
import AppShell from '../components/AppShell'
import PhaseBadge from '../components/PhaseBadge'
import PreviewGate from '../components/PreviewGate'
import GenerationProgress from '../components/GenerationProgress'
import { useJobStream } from '../hooks/useJobStream'
import { api, ApiError } from '../lib/api'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

// Was the pencil codepoint (U+270F + VS16) — default emoji presentation,
// plain stroke SVG instead, same reasoning as Studio.tsx's TabIcon.
function EditIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d="M12.5 3.5l4 4-9 9H3.5v-4z" />
    </svg>
  )
}

// Was the downwards-arrow codepoint (U+2B07) — same default-emoji block as
// the up-arrow Studio.tsx's SuggestionIcon replaced, and this one is live UI
// (a real download button), not just a comment.
function DownloadIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d="M10 3v9M6.5 9L10 12.5 13.5 9M4 16h12" />
    </svg>
  )
}

// suggestActions' own SuggestedAction used to carry an emoji glyph per
// action (clapper board, repeat arrows, smiling face, sparkles, up arrow)
// — plain stroke SVGs instead, same reasoning as the two icons above.
// Moved here from Studio.tsx along with the suggestion chips themselves —
// see this file's own doc on why that block now lives here.
function SuggestionIcon({ kind, className }: { kind: SuggestedAction['kind']; className?: string }) {
  const common = { viewBox: '0 0 20 20', fill: 'none', stroke: 'currentColor', strokeWidth: 1.6, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const, className }
  switch (kind) {
    case 'to-video':
      return (
        <svg {...common}>
          <rect x="2.5" y="5" width="10.5" height="10" rx="1.5" />
          <path d="M13 8.3l4.5-2.3v8l-4.5-2.3" />
        </svg>
      )
    case 'more-batch':
      return (
        <svg {...common}>
          <path d="M4 8a6 6 0 0 1 10.5-3.5M16 12a6 6 0 0 1-10.5 3.5" />
          <path d="M14.5 4.5v3.5H11M5.5 15.5V12H9" />
        </svg>
      )
    case 'save-character':
    case 'save-frame-character':
      return (
        <svg {...common}>
          <circle cx="10" cy="6.5" r="3" />
          <path d="M4 17c0-3.3 2.7-5.5 6-5.5s6 2.2 6 5.5" />
        </svg>
      )
    case 'to-sequence':
      return (
        <svg {...common}>
          <rect x="1.75" y="8.25" width="3.5" height="3.5" rx="0.8" />
          <rect x="8.25" y="8.25" width="3.5" height="3.5" rx="0.8" />
          <rect x="14.75" y="8.25" width="3.5" height="3.5" rx="0.8" />
          <path d="M5.25 10h3M11.75 10h3" />
        </svg>
      )
    case 'upgrade-2k':
      return (
        <svg {...common}>
          <path d="M10 16V4M5.5 8.5L10 4l4.5 4.5" />
        </svg>
      )
  }
}

// PRD §19.4.6's job detail / DAG view — fully reconstructible from the URL
// alone: fetches job + spec fresh from GET /jobs/{bizID} rather than relying
// on any client-side state. Now the sole place a submitted job is actually
// tracked — Studio navigates straight here (via /jobs) on submit instead of
// showing anything inline, since a user is generally trying to start the
// next generation right after clicking Generate, not stare at this one.
export default function JobDetail() {
  const { t } = useTranslation()
  const { bizId } = useParams<{ bizId: string }>()
  const navigate = useNavigate()
  const pushToast = useToast()

  const jobQuery = useJobStream(bizId)

  useEffect(() => {
    if (jobQuery.isError) {
      pushToast(t('jobDetail.connectionUnstable'), () => jobQuery.refetch())
    }
  }, [jobQuery.isError, jobQuery.refetch, pushToast, t])

  const job = jobQuery.data
  const gateNode = job?.nodes.find((n) => n.name === 'gate')
  const gateSuspended = job?.workflow_name === 'video.sequence' && gateNode?.phase === 'Suspended'
  const assetIds = job ? resultAssetIds(job, resolveTab(job.workflow_name)) : []
  const running = job && job.status !== 'succeeded' && job.status !== 'failed' && job.status !== 'cancelled'
  const label = job ? t(WORKFLOW_LABEL_KEY[resolveTab(job.workflow_name)]) || job.workflow_name : ''
  // JobResponse itself carries no started_at (only JobSummary, the list
  // endpoint's lighter projection, does) — the earliest of its nodes'
  // started_at is real generation-start time, same data either way.
  const earliestNodeStartedAt = job?.nodes.map((n) => n.started_at).filter((s): s is string => !!s).sort()[0]

  // F7.4: stops the run and lets the existing terminal-phase machinery
  // (jobsvc.Service.Cancel's own doc) settle credits — this just fires the
  // request and refetches, no optimistic update, since the job's actual
  // status still only flips once the engine and projection layer catch up.
  const cancelJob = useMutation({
    mutationFn: () => api.cancelJob(bizId!),
    onSuccess: () => jobQuery.refetch(),
    onError: (err) => pushToast(err instanceof ApiError ? err.message : t('jobDetail.cancelFailed'), () => cancelJob.mutate()),
  })
  // jobsvc.Service.Delete's own doc: soft-delete, terminal jobs only —
  // leaves this page for the 作业 list rather than refetching a job that no
  // longer shows up there.
  const deleteJob = useMutation({
    mutationFn: () => api.deleteJob(bizId!),
    onSuccess: () => navigate('/jobs'),
    onError: (err) => pushToast(err instanceof ApiError ? err.message : t('jobDetail.deleteFailed'), () => deleteJob.mutate()),
  })

  const suggestions = job && job.status === 'succeeded' ? suggestActions(job, resolveTab(job.workflow_name), assetIds, t) : []

  // "再来一批" used to just call Studio's own createJob.mutate() again with
  // whatever was already in its form — here there's no live form to reuse,
  // so it resubmits job.spec directly (identical to "以此再生成" above,
  // minus the round trip through Studio) and lands on the same "go watch it
  // in the queue" flow every other submission does.
  const moreBatch = useMutation({
    mutationFn: () => api.createJob(resolveTab(job!.workflow_name), job!.spec, crypto.randomUUID(), job!.project_id || undefined),
    onSuccess: (res) => navigate('/jobs', { state: { highlightBizId: res.biz_id } }),
    onError: (err) => pushToast(err instanceof ApiError ? err.message : t('studio.errors.submitFailed'), () => moreBatch.mutate()),
  })

  function applySuggestion(action: SuggestedAction) {
    switch (action.kind) {
      case 'more-batch':
        moreBatch.mutate()
        break
      case 'save-character':
      case 'save-frame-character':
        navigate('/characters', { state: { prefillAssetId: action.sourceAssetId } })
        break
      // to-video/to-sequence/upgrade-2k need actual compose-form input
      // (a video prompt, reviewing shots, confirming the 2K price) rather
      // than a blind resubmit, so these hand off to Studio the same way
      // prefillJob does — see that file's own matching effect.
      default:
        navigate('/', { state: { prefillSuggestion: action } })
    }
  }

  return (
    <AppShell>
      <div className="mx-auto max-w-5xl space-y-6 px-6 py-8">
        {/* Same bug as AssetDetail.tsx, found alongside it: this only ever
            checked `!job`, so a bizId that never successfully loads (bad
            link, or a job belonging to another user — jobsvc.Get's own
            user_id filter) rendered "loading..." forever. Distinct from a
            *transient* error on a job that already loaded once — `job`
            keeps its last-known-good data through that (react-query's
            normal behavior), so this branch only fires when nothing has
            ever loaded at all; the existing toast+refetch above still
            handles the transient case without replacing the page. */}
        {!job && !jobQuery.isError && <p className="text-zinc-500">{t('common.loading')}</p>}
        {!job && jobQuery.isError && <p className="text-zinc-500">{t('jobDetail.notFound')}</p>}

        {job && (
          <>
            {/* Was a plain `justify-between` row with no width constraint on
                either side — a long title (job.title is truncated at 128
                chars server-side, still often long) had nothing forcing it
                to give way, so it squeezed the button group down to almost
                nothing and each button's own text wrapped inside its now-tiny
                box instead of the row just wrapping to a second line. min-w-0
                lets the title truncate instead of stretching; shrink-0 +
                whitespace-nowrap keeps every button and the badge at their
                natural width regardless; flex-wrap is the fallback once even
                that doesn't fit a narrow viewport. */}
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="min-w-0">
                <h1 className="truncate text-lg font-medium">
                  {!!job.retry_of_job_id && (
                    <Link
                      to={`/jobs/${job.retry_of_job_id}`}
                      title={t('jobs.retryIndicatorLinkTitle')}
                      className="mr-1 text-violet-400 hover:text-violet-300"
                      onClick={(e) => e.stopPropagation()}
                    >
                      ↻
                    </Link>
                  )}
                  {job.title || label}
                </h1>
                <p className="text-sm text-zinc-500">{label}</p>
              </div>
              <div className="flex shrink-0 flex-wrap items-center gap-3">
                <PhaseBadge phase={statusToPhase(job.status)} />
                {/* §07's "「以此再生成」在作業詳情頁本身缺獨立入口" gap —
                    AssetDetail already has this (regenerate()'s own doc
                    there); this is the same prefillJob round trip, just
                    triggered from the job itself rather than one of its
                    output assets, so it's available even for a job that
                    partially/fully failed and has no asset to click through. */}
                <button
                  onClick={() => navigate('/', { state: { prefillJob: { workflowName: job.workflow_name, spec: job.spec } } })}
                  className="flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg border border-zinc-700 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-violet-500 hover:text-violet-300"
                >
                  <EditIcon className="h-3.5 w-3.5" />
                  {t('assetDetail.regenerateFromThis')}
                </button>
                {running && (
                  <button
                    onClick={() => cancelJob.mutate()}
                    disabled={cancelJob.isPending}
                    className="shrink-0 whitespace-nowrap rounded-lg border border-zinc-700 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-red-500 hover:text-red-400 disabled:opacity-50"
                  >
                    {cancelJob.isPending ? t('jobDetail.cancelling') : t('jobDetail.cancelJob')}
                  </button>
                )}
                {!running && (
                  <button
                    onClick={() => deleteJob.mutate()}
                    disabled={deleteJob.isPending}
                    className="shrink-0 whitespace-nowrap rounded-lg border border-zinc-700 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-red-500 hover:text-red-400 disabled:opacity-50"
                  >
                    {deleteJob.isPending ? t('jobDetail.deleting') : t('jobDetail.deleteJob')}
                  </button>
                )}
              </div>
            </div>

            {/* §07's "已消耗 / 預估 對比條" gap — job.credit_held is what's
                currently reserved, credit_settled is what's actually been
                spent so far (both already tracked since W7, just not
                echoed on this endpoint until now — handleGetJob's own
                doc). Only worth showing once something has actually been
                held; a job that hasn't started yet has nothing to compare.
                job.credit_held is a snapshot written once at job creation —
                creditsvc.Refund only ever adjusts credit_accounts (the
                user's real balance/held pool) and the ledger, never this
                column, so it stays frozen at the original estimate forever.
                For a still-running job that's accurate (nothing's been
                refunded yet); for a terminal one it isn't — showing it
                unchanged here read as "your credits are still stuck",
                found live off a real failed job whose unspent hold had
                already been refunded to the user's actual balance. Once
                terminal, show what actually happened instead: the
                difference (if any) was refunded, not still held. */}
            {job.credit_held > 0 && (
              <div className="flex items-center gap-4 rounded-xl border border-zinc-800 bg-zinc-900/60 px-4 py-2.5 font-mono text-xs text-zinc-400">
                <span>
                  {t('jobDetail.creditSettled')} <span className="text-zinc-200">✦{job.credit_settled}</span>
                </span>
                <span className="text-zinc-700">/</span>
                {running ? (
                  <span>
                    {t('jobDetail.creditHeld')} <span className="text-zinc-200">✦{job.credit_held}</span>
                  </span>
                ) : (
                  job.credit_held > job.credit_settled && (
                    <span>
                      {t('jobDetail.creditRefunded')}{' '}
                      <span className="text-zinc-200">✦{job.credit_held - job.credit_settled}</span>
                    </span>
                  )
                )}
                {job.credit_estimated > 0 && (
                  <>
                    <span className="text-zinc-700">/</span>
                    <span>
                      {t('jobDetail.creditEstimated')} <span className="text-zinc-200">✦{job.credit_estimated}</span>
                    </span>
                  </>
                )}
              </div>
            )}

            {/* Used to also show a "实时连接不稳定，重新连接中" message on
                streamState === 'reconnecting' — removed, not just fixed
                (§19.5.3's own original intent was "静默降级...不用 Toast"
                anyway): SSE going quiet silently falls back to polling with
                zero functional difference to the user, so the message was
                pure alarm with nothing actionable behind it, and the
                "仍在处理...可能正在自动重试" line in GenerationProgress
                itself already covers the "is this actually still working"
                reassurance the user might want ("意义不明", found live).
                useJobStream's watchdog/reconnecting detection still runs
                exactly as before — only this display is gone. */}
            {job.status !== 'succeeded' && job.status !== 'failed' && !gateSuspended && (
              <div className="rounded-xl border border-zinc-800 bg-zinc-900 p-6">
                <GenerationProgress kind={job.workflow_name.startsWith('video') ? 'video' : 'image'} startedAt={earliestNodeStartedAt} />
              </div>
            )}

            {gateSuspended && bizId && (
              <div className="rounded-xl border border-zinc-800 bg-zinc-900 p-6">
                <PreviewGate
                  bizId={bizId}
                  job={job}
                  duration={job.spec.duration_seconds ?? 5}
                  onResumed={() => jobQuery.refetch()}
                />
              </div>
            )}

            {job.status === 'failed' && (
              <div className="flex flex-col items-center gap-3 rounded-xl border border-zinc-800 bg-zinc-900 p-6 text-center">
                <p className="text-red-400">{displayNodeError(firstSpecificError(job.nodes), t)}</p>
                <button
                  onClick={() => moreBatch.mutate()}
                  disabled={moreBatch.isPending}
                  className="rounded-full border border-zinc-700 px-4 py-1.5 text-xs text-zinc-200 transition hover:border-violet-500 hover:text-violet-300 disabled:opacity-50"
                >
                  {t('studio.retryGeneration')}
                </button>
              </div>
            )}

            {job.status === 'succeeded' && assetIds.length > 0 && (
              <div className="space-y-4 rounded-xl border border-zinc-800 bg-zinc-900 p-6">
                <div className="flex flex-wrap gap-3">
                  {assetIds.map((id) => (
                    <ResultAsset key={id} assetId={id} />
                  ))}
                </div>

                {suggestions.length > 0 && (
                  <div className="flex flex-wrap items-center gap-2 border-t border-zinc-800 pt-4">
                    <span className="text-xs text-zinc-500">{t('studio.suggestionsLabel')}</span>
                    {suggestions.map((s) => (
                      <button
                        key={s.kind}
                        onClick={() => applySuggestion(s)}
                        disabled={s.kind === 'more-batch' && moreBatch.isPending}
                        className="flex items-center gap-1.5 rounded-full border border-zinc-700 bg-zinc-950 px-3 py-1.5 text-xs text-zinc-200 transition hover:border-violet-500 hover:text-violet-300 disabled:opacity-50"
                      >
                        <SuggestionIcon kind={s.kind} className="h-3.5 w-3.5" />
                        {s.label}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            )}
          </>
        )}
      </div>
    </AppShell>
  )
}

// §07's "下載已完成功能仍缺" gap — AssetDetail already has this exact
// `<a download>` pattern for a single asset viewed on its own page; this is
// the same thing inline on the result grid so downloading doesn't require
// an extra click through to /assets/:id first.
// Previously download-only — the only way to publish an asset to Community
// (or save it as a character, or see anything beyond the raw file) is
// AssetDetail's own page, and this had no link to it, and no publish
// affordance of its own either. That was already true before Studio stopped
// showing results inline, but this is now the one place most users actually
// see a fresh result, so the gap became a lot more visible (found live:
// "不能 publish 视频到 community 吗" — the capability was always there
// server-side, just not reachable from here). Publishing directly here
// (same api.setAssetPublic call AssetDetail's own setPublic mutation makes)
// beats only linking through to AssetDetail: a video needs `controls` for
// inline playback, which would fight a whole-card navigate-away Link.
function ResultAsset({ assetId }: { assetId: string }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const pushToast = useToast()
  const { data, isError } = useQuery({ queryKey: ['asset', assetId], queryFn: () => api.getAsset(assetId) })
  const setPublic = useMutation({
    mutationFn: (isPublic: boolean) => api.setAssetPublic(assetId, isPublic),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['asset', assetId] }),
    onError: () => pushToast(t('community.publishFailed')),
  })
  if (isError) {
    return (
      <div className="flex h-48 w-48 items-center justify-center rounded-lg border border-dashed border-zinc-800 text-center text-xs text-zinc-600">
        {t('jobDetail.assetMissing')}
      </div>
    )
  }
  if (!data) return <div className="h-48 w-48 animate-pulse rounded-lg bg-zinc-800" />
  return (
    <div className="space-y-1.5">
      <div className="group relative">
        {data.type === 'video' ? (
          <video src={data.public_url} controls className="h-48 w-48 rounded-lg bg-black object-contain" />
        ) : (
          <img src={data.public_url} alt="" className="h-48 w-48 rounded-lg object-cover" />
        )}
        <a
          href={data.public_url}
          download
          target="_blank"
          rel="noreferrer"
          title={t('assetDetail.download')}
          className="absolute right-1.5 top-1.5 rounded-lg bg-black/60 p-1.5 text-white opacity-0 backdrop-blur transition group-hover:opacity-100"
        >
          <DownloadIcon className="h-3.5 w-3.5" />
        </a>
      </div>
      <div className="flex items-center justify-between gap-2 text-xs">
        <button
          onClick={() => setPublic.mutate(!data.is_public)}
          disabled={setPublic.isPending}
          className={`rounded-lg border px-2 py-1 transition disabled:opacity-50 ${
            data.is_public
              ? 'border-violet-700 text-violet-300 hover:border-violet-500'
              : 'border-zinc-700 text-zinc-300 hover:border-zinc-600'
          }`}
        >
          {data.is_public ? t('community.unpublish') : t('community.publish')}
        </button>
        <Link to={`/assets/${assetId}`} className="text-zinc-500 hover:text-zinc-300">
          {t('jobDetail.viewAsset')}
        </Link>
      </div>
    </div>
  )
}
