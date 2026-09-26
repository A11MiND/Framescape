import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Copy, Heart, Sparkles } from 'lucide-react'
import { Button, IconButton, buttonClasses, cn } from '../../ui'
import type { CommunityWork } from '../../lib/api/community'
import { formatDate } from '../../lib/format'
import { useToast } from '../../components/Toast'
import { workTitle } from './helpers'

/**
 * One published work: the media, its public prompt, likes and "create
 * similar", which carries only the prompt. The author can unpublish it.
 */
export function WorkViewer({
  work,
  signedIn,
  liking,
  unpublishing,
  onLike,
  onUnpublish,
}: {
  work: CommunityWork
  signedIn: boolean
  liking: boolean
  unpublishing: boolean
  onLike: () => void
  onUnpublish: () => void
}) {
  const { t, i18n } = useTranslation('community')
  const navigate = useNavigate()
  const toast = useToast()
  const prompt = (work.prompt ?? '').trim()
  const video = work.type === 'video'

  const similar = () =>
    navigate(`/create/${video ? 'video' : 'image'}`, { state: { prefillJob: { workflowName: video ? 'video.single' : 'image.single', spec: { text: prompt } } } })
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(prompt)
      toast(t('viewer.copied'))
    } catch {
      // the prompt stays selectable on the page
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex max-h-[60dvh] items-center justify-center overflow-hidden rounded-card bg-black">
        {video ? (
          <video src={work.public_url} poster={work.thumb_url || undefined} controls playsInline className="max-h-[60dvh] w-full" />
        ) : (
          <img src={work.public_url} alt={workTitle(t, work)} className="max-h-[60dvh] w-full object-contain" />
        )}
      </div>
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <p className="text-body font-semibold break-words text-fg">{workTitle(t, work)}</p>
          <p className="text-caption text-fg-muted">
            {t(`kind.${video ? 'video' : 'image'}`)} · {t('viewer.published', { time: formatDate(work.published_at, i18n.language) })}
          </p>
        </div>
        <Button
          aria-pressed={work.liked}
          loading={liking}
          icon={<Heart aria-hidden className={cn('size-4', work.liked && 'fill-current text-danger-icon')} />}
          onClick={onLike}
        >
          <span className="tabular-nums">{work.like_count}</span>
          <span className="sr-only">{work.liked ? t('unlike') : t('like')}</span>
        </Button>
      </div>
      {!signedIn && (
        <p className="rounded-card bg-surface-2 px-3 py-2 text-caption text-fg-muted">
          {t('guest.like')}{' '}
          <Link to="/login" className="font-medium text-primary-text hover:underline">
            {t('guest.login')}
          </Link>
        </p>
      )}
      <section className="flex flex-col gap-1.5">
        <div className="flex items-center justify-between">
          <h3 className="text-label font-medium text-fg">{t('viewer.prompt')}</h3>
          {prompt && <IconButton size="sm" label={t('viewer.copy')} icon={<Copy className="size-4" />} onClick={() => void copy()} />}
        </div>
        <p className="max-h-40 overflow-y-auto rounded-card border border-border p-3 text-body whitespace-pre-wrap text-fg">{prompt || t('viewer.noPrompt')}</p>
      </section>
      <div className="flex flex-col gap-1.5">
        <Button variant="primary" icon={<Sparkles aria-hidden className="size-4" />} disabled={!prompt} onClick={similar}>
          {t('viewer.similar')}
        </Button>
        <p className="text-caption text-fg-muted">{t('viewer.similarHint')}</p>
      </div>
      {work.mine && (
        <div className="flex flex-col gap-2 rounded-card border border-border p-3">
          <p className="text-caption text-fg-muted">{t('viewer.mine')}</p>
          <div className="flex flex-wrap gap-2">
            <Link to={`/assets/${work.biz_id}`} className={buttonClasses('secondary', 'sm')}>
              {t('viewer.openAsset')}
            </Link>
            <Button size="sm" variant="danger-outline" loading={unpublishing} onClick={onUnpublish}>
              {t('viewer.unpublish')}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
