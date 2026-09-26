import { useEffect, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Gift } from 'lucide-react'
import { Button, Card, Field, SegmentedControl, Select, buttonClasses } from '../../ui'
import { createApi, type CreateRequest } from '../../lib/api/create'
import { projectsApi } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { getDeviceId } from '../../lib/deviceId'
import { useAuthStore } from '../../lib/authStore'
import { useMe } from '../../app/useMe'
import { useCurrentProject } from '../../app/currentProject'
import { useToast } from '../../components/Toast'
import { CreateNav } from './CreateNav'
import { PromptInput } from './PromptInput'
import { ReferencePicker } from './ReferencePicker'
import { CharacterPicker } from './CharacterPicker'
import { PresetPicker } from './PresetPicker'
import { CostBar } from './CostBar'
import { ResultPane } from './ResultPane'
import { useDraft } from './drafts'
import { useSubmit } from './useSubmit'
import { characterSlots, readPrefill } from './prefill'
import { useCharacterPrefill } from './useCharacterPrefill'
import { usePresetPrefill } from './usePresetPrefill'

const COUNTS = [1, 2, 4, 6, 9]

interface ImageDraft {
  text: string
  n: number
  reference: string[]
  characters: string[]
  presets: string[]
  project: string
  lastJob: string
}

const INITIAL: ImageDraft = { text: '', n: 1, reference: [], characters: [], presets: [], project: '', lastJob: '' }

function useGuestTrial(text: string) {
  const { t } = useTranslation('create')
  const navigate = useNavigate()
  const toast = useToast()
  const [image, setImage] = useState<string | null>(null)
  const trial = useMutation({
    mutationFn: () => createApi.trial(text.trim(), getDeviceId()),
    onSuccess: (res) => setImage(res.image_url),
    onError: (err) => toast(errorText(t, err)),
  })
  return {
    action: (
      <Button variant="primary" size="lg" loading={trial.isPending} disabled={!text.trim()} onClick={() => trial.mutate()}>
        {t('action.trial')}
      </Button>
    ),
    pane: (
      <Card padding="lg" className="flex flex-col items-center gap-3 text-center">
        {image ? (
          <>
            <h3 className="text-section font-semibold text-fg">{t('guest.result')}</h3>
            <img src={image} alt="" className="max-h-[480px] w-full rounded-card object-contain" />
          </>
        ) : (
          <>
            <Gift aria-hidden className="size-8 text-primary-text" />
            <h3 className="text-section font-semibold text-fg">{t('guest.title')}</h3>
          </>
        )}
        <p className="text-body text-fg-muted">{t('guest.body')}</p>
        <Button onClick={() => navigate('/login', { state: { from: '/create/image' } })}>{t('guest.signIn')}</Button>
      </Card>
    ),
  }
}

/** Single and batch images (spec P01): composer and results side by side. */
export default function ImageStudio() {
  const { t } = useTranslation('create')
  const location = useLocation()
  const navigate = useNavigate()
  const signedIn = useAuthStore((s) => Boolean(s.accessToken))
  const me = useMe()
  const [currentProject] = useCurrentProject(me.data?.biz_id)
  const [draft, update] = useDraft<ImageDraft>(me.data?.biz_id, 'image', INITIAL)
  useCharacterPrefill(draft.characters, (characters) => update({ characters }))
  usePresetPrefill(draft.presets, (presets) => update({ presets }))
  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities, staleTime: 60_000 })
  const characters = useQuery({ queryKey: keys.characters, queryFn: createApi.characters, enabled: signedIn })
  const presets = useQuery({ queryKey: keys.presets, queryFn: createApi.presets, enabled: signedIn })
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list, enabled: signedIn })

  // A prefill (create again, a character to use) seeds the draft once.
  useEffect(() => {
    const p = readPrefill(location.state, 'image.single')
    if (!p) return
    update(p)
    navigate('.', { replace: true, state: {} })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state])

  const maxChars = caps.data?.image.max_prompt_chars ?? 1500
  const maxN = caps.data?.image.max_n ?? 9
  const project = draft.project || currentProject || ''
  const text = draft.text.trim()
  const tooLong = [...draft.text].length > maxChars
  const request: CreateRequest | null =
    signedIn && text && !tooLong
      ? {
          workflow_name: 'image.single',
          spec: {
            text: draft.text,
            ...(draft.n > 1 ? { n: draft.n } : {}),
            ...(draft.reference[0] ? { source_image_asset_id: draft.reference[0] } : {}),
            ...(draft.characters.length ? { characters: characterSlots(draft.characters) } : {}),
            ...(draft.presets.length ? { preset_ids: draft.presets } : {}),
          },
          ...(project ? { project_id: project } : {}),
        }
      : null
  const job = useSubmit(request, (bizId) => update({ lastJob: bizId }))
  const guest = useGuestTrial(draft.text)

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6">
      <CreateNav mode="image" />
      <div className="grid items-start gap-6 xl:grid-cols-[400px_minmax(0,1fr)]">
        <Card padding="none" className="flex flex-col gap-5 p-4 lg:p-5">
          <PromptInput
            label={t('prompt.label')}
            value={draft.text}
            onChange={(v) => update({ text: v })}
            maxChars={maxChars}
            canRewrite={signedIn}
            characters={signedIn ? (characters.data?.characters ?? []) : undefined}
            onMentionCharacter={(c) => !draft.characters.includes(c.biz_id) && update({ characters: [...draft.characters, c.biz_id].slice(0, 6) })}
            onMentionAsset={
              signedIn
                ? (a) => {
                    update({ reference: [a.biz_id] })
                    return t('prompt.imageN', { n: 1 })
                  }
                : undefined
            }
            error={tooLong ? t('prompt.tooLong', { max: maxChars }) : undefined}
          />
          {!draft.text && (
            <button type="button" onClick={() => update({ text: t('prompt.example') })} className="-mt-3 w-fit text-caption text-primary-text hover:underline">
              {t('prompt.useExample')}
            </button>
          )}
          {signedIn && (
            <>
              <ReferencePicker label={t('reference.label')} value={draft.reference} onChange={(ids) => update({ reference: ids.slice(-1) })} limits={{ max: 1 }} />
              <div className="grid gap-4 sm:grid-cols-2 [&>*]:min-w-0">
                <CharacterPicker characters={characters.data?.characters ?? []} value={draft.characters} onChange={(ids) => update({ characters: ids })} />
                <PresetPicker presets={presets.data?.presets ?? []} value={draft.presets} onChange={(ids) => update({ presets: ids })} />
              </div>
              <div className="grid gap-4 sm:grid-cols-2 [&>*]:min-w-0">
                <div className="flex flex-col gap-1.5">
                  <span className="text-label font-medium text-fg">
                    {t('count.label')}
                  </span>
                  <SegmentedControl
                    fullWidth
                    label={t('count.label')}
                    value={String(draft.n)}
                    onChange={(v) => update({ n: Number(v) })}
                    options={COUNTS.filter((c) => c <= maxN).map((c) => ({ value: String(c), label: String(c) }))}
                  />
                </div>
                <Field label={t('project.label')}>
                  <Select value={project} onChange={(e) => update({ project: e.target.value })}>
                    <option value="">{t('project.none')}</option>
                    {(projects.data?.projects ?? []).map((p) => (
                      <option key={p.biz_id} value={p.biz_id}>
                        {p.name}
                      </option>
                    ))}
                  </Select>
                </Field>
              </div>
            </>
          )}
          {signedIn ? (
            <CostBar
              ready={Boolean(request)}
              credits={job.estimate?.data?.credits_total}
              calculating={Boolean(request) && job.stale}
              failed={Boolean(job.estimate?.isError)}
              reason={job.estimateError}
              notice={job.notice}
              actionLabel={t('action.generate')}
              onAction={job.submit}
              loading={job.submitting}
              disabled={!job.canSubmit}
            />
          ) : (
            <div className="flex items-center justify-between gap-3 border-t border-border pt-3">
              <Link to="/login" state={{ from: '/create/image' }} className={buttonClasses('ghost', 'sm')}>
                {t('action.signIn')}
              </Link>
              {guest.action}
            </div>
          )}
        </Card>
        <ResultPane jobId={draft.lastJob || null}>{signedIn ? undefined : guest.pane}</ResultPane>
      </div>
    </div>
  )
}
