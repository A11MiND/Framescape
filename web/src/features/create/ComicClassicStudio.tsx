import { useEffect } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { BookOpen } from 'lucide-react'
import { Card, Field, SegmentedControl, Select, TabList, TabPanel, Tabs, Textarea } from '../../ui'
import { createApi, type CreateRequest } from '../../lib/api/create'
import { projectsApi } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { useMe } from '../../app/useMe'
import { useCurrentProject } from '../../app/currentProject'
import { CreateNav } from './CreateNav'
import { ReferencePicker } from './ReferencePicker'
import { CharacterPicker } from './CharacterPicker'
import { PresetPicker } from './PresetPicker'
import { CostBar } from './CostBar'
import { ResultPane } from './ResultPane'
import { useDraft } from './drafts'
import { useSubmit } from './useSubmit'
import { characterSlots, readPrefill } from './prefill'

type Source = 'panels' | 'story'
type Provider = 'minimax' | 'gemini'

interface ClassicDraft {
  source: Source
  count: number
  panels: string[]
  story: string
  provider: Provider
  reference: string[]
  characters: string[]
  presets: string[]
  project: string
  lastJob: string
}

const MIN_PANELS = 2
const INITIAL: ClassicDraft = {
  source: 'panels', count: 4, panels: [], story: '', provider: 'minimax',
  reference: [], characters: [], presets: [], project: '', lastJob: '',
}

/** Classic comic (spec P07): MiniMax or Gemini draws the page from written panels or a story. */
export default function ComicClassicStudio() {
  const { t } = useTranslation('create')
  const location = useLocation()
  const navigate = useNavigate()
  const me = useMe()
  const [currentProject] = useCurrentProject(me.data?.biz_id)
  const [draft, update] = useDraft<ClassicDraft>(me.data?.biz_id, 'comic-classic', INITIAL)
  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities, staleTime: 60_000 })
  const characters = useQuery({ queryKey: keys.characters, queryFn: createApi.characters })
  const presets = useQuery({ queryKey: keys.presets, queryFn: createApi.presets })
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list })

  // "Create again" from a classic comic seeds the draft once.
  useEffect(() => {
    const p = readPrefill(location.state, 'image.comic4')
    if (!p || p.mode === 'direct') return
    update({
      source: p.panels?.length ? 'panels' : 'story',
      count: p.panels?.length || p.n || INITIAL.count,
      panels: p.panels ?? [],
      story: p.story ?? '',
      reference: p.reference ?? [],
      characters: p.characters ?? [],
      presets: p.presets ?? [],
      ...(p.provider === 'minimax' ? { provider: 'minimax' as const } : {}),
    })
    navigate('.', { replace: true, state: {} })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state])

  const maxPanels = caps.data?.image.max_n ?? 9
  const maxChars = caps.data?.image.max_prompt_chars ?? 1500
  const geminiOn = Boolean(caps.data?.providers?.gemini.enabled)
  const provider: Provider = draft.provider === 'gemini' && geminiOn ? 'gemini' : 'minimax'
  const count = Math.min(Math.max(draft.count, MIN_PANELS), maxPanels)
  const panels = Array.from({ length: count }, (_, i) => draft.panels[i] ?? '')
  const project = draft.project || currentProject || ''
  const tooLong = draft.source === 'panels' ? panels.some((p) => [...p].length > maxChars) : false
  const filled = draft.source === 'panels' ? panels.every((p) => p.trim()) : Boolean(draft.story.trim())

  const request: CreateRequest | null =
    filled && !tooLong
      ? {
          workflow_name: 'image.comic4',
          spec: {
            ...(draft.source === 'panels' ? { panels: panels.map((p) => p.trim()) } : { story: draft.story, n: count }),
            ...(provider === 'gemini' ? { image_provider: 'gemini' } : {}),
            ...(draft.reference[0] ? { source_image_asset_id: draft.reference[0] } : {}),
            ...(draft.characters.length ? { characters: characterSlots(draft.characters) } : {}),
            ...(draft.presets.length ? { preset_ids: draft.presets } : {}),
          },
          ...(project ? { project_id: project } : {}),
        }
      : null
  const job = useSubmit(request, (bizId) => update({ lastJob: bizId }))
  const setPanel = (i: number, text: string) => {
    const next = [...panels]
    next[i] = text
    update({ panels: next })
  }

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6">
      <CreateNav mode="comic-classic" />
      <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,1.25fr)_minmax(0,1fr)]">
        <Card padding="none" className="flex flex-col gap-5 p-4 lg:p-5">
          <header className="flex items-start gap-3">
            <span className="inline-flex size-10 shrink-0 items-center justify-center rounded-card bg-primary-soft text-primary-text">
              <BookOpen aria-hidden className="size-5" />
            </span>
            <div className="min-w-0">
              <h1 className="text-section font-semibold text-fg">{t('classic.title')}</h1>
              <p className="text-caption text-fg-muted">{t('classic.description')}</p>
            </div>
          </header>

          <div className="grid gap-4 sm:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)] [&>*]:min-w-0">
            <div className="flex flex-col gap-1.5">
              <span className="text-label font-medium text-fg">{t('classic.count')}</span>
              <SegmentedControl
                fullWidth
                label={t('classic.count')}
                value={String(count)}
                onChange={(v) => update({ count: Number(v) })}
                options={Array.from({ length: maxPanels - MIN_PANELS + 1 }, (_, i) => ({ value: String(i + MIN_PANELS), label: String(i + MIN_PANELS) }))}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <span className="text-label font-medium text-fg">{t('classic.provider.label')}</span>
              <SegmentedControl<Provider>
                fullWidth
                label={t('classic.provider.label')}
                value={provider}
                onChange={(v) => update({ provider: v })}
                options={[
                  { value: 'minimax', label: t('classic.provider.minimax') },
                  { value: 'gemini', label: t('classic.provider.gemini'), disabled: !geminiOn },
                ]}
              />
              {!geminiOn && <p className="text-caption text-fg-muted">{t('classic.provider.geminiOff')}</p>}
            </div>
          </div>

          <Tabs value={draft.source} onValueChange={(v) => update({ source: v as Source })}>
            <TabList
              label={t('classic.tab.label')}
              items={[
                { value: 'panels', label: t('classic.tab.panels') },
                { value: 'story', label: t('classic.tab.story') },
              ]}
            />
            <TabPanel value="panels" className="flex flex-col gap-3 pt-4">
              <h2 className="text-label font-semibold text-fg">{t('classic.panels', { n: count })}</h2>
              <ol className="grid gap-3 sm:grid-cols-2">
                {panels.map((text, i) => (
                  <li key={i} className="flex flex-col gap-2 rounded-card border border-border bg-surface p-3">
                    <span className="inline-flex size-7 items-center justify-center rounded-full bg-primary-soft text-body font-semibold text-primary-text tabular-nums">{i + 1}</span>
                    <Textarea aria-label={t('classic.panel', { n: i + 1 })} value={text} maxChars={maxChars} placeholder={t('classic.panelPlaceholder')} onChange={(e) => setPanel(i, e.target.value)} className="min-h-24" />
                  </li>
                ))}
              </ol>
              {!filled && panels.some((p) => p.trim()) && <p className="text-caption text-warning-fg">{t('classic.fillAll')}</p>}
            </TabPanel>
            <TabPanel value="story" className="pt-4">
              <Field label={t('classic.story.label')} help={t('classic.story.help')}>
                <Textarea value={draft.story} maxChars={20000} placeholder={t('classic.story.placeholder', { n: count })} onChange={(e) => update({ story: e.target.value })} className="min-h-40" />
              </Field>
            </TabPanel>
          </Tabs>

          <div className="grid gap-4 sm:grid-cols-2 [&>*]:min-w-0">
            <ReferencePicker label={t('classic.reference')} value={draft.reference} onChange={(ids) => update({ reference: ids.slice(-1) })} limits={{ max: 1 }} />
            <PresetPicker presets={presets.data?.presets ?? []} value={draft.presets} onChange={(ids) => update({ presets: ids })} />
          </div>
          <div className="grid gap-4 sm:grid-cols-2 [&>*]:min-w-0">
            <div className="flex flex-col gap-1.5">
              <CharacterPicker characters={characters.data?.characters ?? []} value={draft.characters} onChange={(ids) => update({ characters: ids })} />
              <p className="text-caption text-fg-muted">{t('classic.charactersHelp')}</p>
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
          <p className="text-caption text-fg-muted">{t('classic.baked')}</p>

          <CostBar
            ready={Boolean(request)}
            credits={job.estimate?.data?.credits_total}
            calculating={Boolean(request) && job.stale}
            failed={Boolean(job.estimate?.isError)}
            reason={job.estimateError}
            notice={job.notice}
            actionLabel={t('classic.generate', { n: count })}
            onAction={job.submit}
            loading={job.submitting}
            disabled={!job.canSubmit}
          />
        </Card>
        <ResultPane jobId={draft.lastJob || null} />
      </div>
    </div>
  )
}
