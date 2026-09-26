import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { ArrowLeft, ArrowRight, FolderOpen, Pencil, Trash2, UserRound } from 'lucide-react'
import { Button, Card, EmptyState, Skeleton, TabList, TabPanel, Tabs } from '../../ui'
import { projectsApi } from '../../lib/api/projects'
import { assetsApi } from '../../lib/api/assets'
import { jobsApi } from '../../lib/api/jobs'
import { createApi } from '../../lib/api/create'
import { keys } from '../../lib/api/keys'
import { ApiError } from '../../lib/api/client'
import { AssetCard } from '../library/AssetCard'
import { TaskList } from '../tasks/TaskItem'
import { ProjectCover } from './ProjectsPage'
import { DeleteProject, ProjectForm } from './ProjectDialogs'

function CharacterThumb({ assetId }: { assetId?: string }) {
  const asset = useQuery({ queryKey: keys.assets.detail(assetId ?? ''), queryFn: () => assetsApi.get(assetId!), enabled: Boolean(assetId) })
  return (
    <span className="flex size-16 shrink-0 items-center justify-center overflow-hidden rounded-thumb bg-surface-2 text-fg-muted">
      {asset.data ? <img src={asset.data.thumb_url || asset.data.public_url} alt="" className="size-full object-cover" /> : <UserRound aria-hidden className="size-6" />}
    </span>
  )
}

/** One project (spec P14): what it is, and its assets, tasks and characters. */
export default function ProjectDetailPage() {
  const { t } = useTranslation('projects')
  const { projectId = '' } = useParams()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const tab = ['tasks', 'characters'].includes(params.get('tab') ?? '') ? params.get('tab')! : 'assets'
  const project = useQuery({ queryKey: keys.projects.detail(projectId), queryFn: () => projectsApi.get(projectId), retry: false })
  const assets = useQuery({ queryKey: [...keys.assets.all, 'list', { project_id: projectId }], queryFn: () => assetsApi.list({ project_id: projectId }), enabled: tab === 'assets' })
  const jobs = useQuery({ queryKey: keys.jobs.list({ project_id: projectId, limit: 50 }), queryFn: () => jobsApi.list({ project_id: projectId, limit: 50 }), enabled: tab === 'tasks' })
  const characters = useQuery({ queryKey: keys.characters, queryFn: createApi.characters, enabled: tab === 'characters' })
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)

  if (project.isPending) return <Skeleton className="mx-4 my-6 h-64" />
  const p = project.data
  if (!p) {
    const missing = project.error instanceof ApiError && project.error.status === 404
    return (
      <div className="mx-auto max-w-[720px] px-4 py-10">
        <Card padding="none">
          <EmptyState icon={<FolderOpen className="size-7" />} title={missing ? t('detail.notFound') : t('empty.title')} body={missing ? t('detail.notFoundBody') : undefined} action={<Button onClick={() => navigate('/projects')}>{t('detail.back')}</Button>} />
        </Card>
      </div>
    )
  }
  const own = (characters.data?.characters ?? []).filter((c) => c.project_id === p.biz_id)

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6">
      <Link to="/projects" className="inline-flex w-fit items-center gap-1.5 text-body text-fg-muted hover:text-fg">
        <ArrowLeft aria-hidden className="size-4" />
        {t('detail.back')}
      </Link>
      <Card padding="none" className="flex flex-col overflow-hidden sm:flex-row">
        <ProjectCover project={p} className="aspect-[16/9] w-full sm:aspect-auto sm:w-72" />
        <div className="flex min-w-0 flex-1 flex-col gap-2 p-4">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <h1 className="min-w-0 text-title font-semibold break-words text-fg">{p.name}</h1>
            <div className="flex gap-2">
              <Button icon={<Pencil aria-hidden className="size-4" />} onClick={() => setEditing(true)}>
                {t('edit')}
              </Button>
              <Button variant="danger-outline" icon={<Trash2 aria-hidden className="size-4" />} onClick={() => setDeleting(true)}>
                {t('delete')}
              </Button>
            </div>
          </div>
          <p className="text-body text-fg-muted">{p.description || t('noDescription')}</p>
          <p className="text-caption text-fg tabular-nums">{t('stats', { assets: p.asset_count, jobs: p.job_count, characters: p.character_count })}</p>
        </div>
      </Card>

      <Tabs
        value={tab}
        onValueChange={(v) => {
          const next = new URLSearchParams(params)
          if (v === 'assets') next.delete('tab')
          else next.set('tab', v)
          setParams(next, { replace: true })
        }}
      >
        <TabList
          label={t('detail.tabs')}
          items={[
            { value: 'assets', label: t('detail.assets'), count: p.asset_count },
            { value: 'tasks', label: t('detail.tasks'), count: p.job_count },
            { value: 'characters', label: t('detail.characters'), count: p.character_count },
          ]}
        />
        <TabPanel value="assets" className="flex flex-col gap-3 pt-4">
          {assets.isPending ? (
            <Skeleton className="h-48 w-full" />
          ) : (assets.data?.assets ?? []).length === 0 ? (
            <EmptyState icon={<FolderOpen className="size-7" />} title={t('detail.emptyAssets')} />
          ) : (
            <>
              <ul className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3 sm:grid-cols-[repeat(auto-fill,minmax(220px,1fr))]">
                {assets.data!.assets.map((a) => (
                  <AssetCard key={a.biz_id} asset={a} projectName={p.name} selecting={false} selected={false} onToggle={() => {}} />
                ))}
              </ul>
              <Link to={`/assets?project=${p.biz_id}`} className="inline-flex w-fit items-center gap-1 text-caption font-medium text-primary-text hover:underline">
                {t('detail.openLibrary')}
                <ArrowRight aria-hidden className="size-3.5" />
              </Link>
            </>
          )}
        </TabPanel>
        <TabPanel value="tasks" className="flex flex-col gap-3 pt-4">
          {jobs.isPending ? (
            <Skeleton className="h-48 w-full" />
          ) : (jobs.data?.jobs ?? []).length === 0 ? (
            <EmptyState icon={<FolderOpen className="size-7" />} title={t('detail.emptyTasks')} />
          ) : (
            <>
              <TaskList jobs={jobs.data!.jobs} />
              <Link to={`/jobs?project=${p.biz_id}`} className="inline-flex w-fit items-center gap-1 text-caption font-medium text-primary-text hover:underline">
                {t('detail.openTasks')}
                <ArrowRight aria-hidden className="size-3.5" />
              </Link>
            </>
          )}
        </TabPanel>
        <TabPanel value="characters" className="pt-4">
          {characters.isPending ? (
            <Skeleton className="h-32 w-full" />
          ) : own.length === 0 ? (
            <EmptyState icon={<UserRound className="size-7" />} title={t('detail.emptyCharacters')} />
          ) : (
            <ul className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-3">
              {own.map((c) => (
                <li key={c.biz_id} className="flex items-center gap-3 rounded-card border border-border bg-surface p-3">
                  <CharacterThumb assetId={c.ref_asset_ids[0]} />
                  <div className="min-w-0">
                    <p className="truncate text-body font-semibold text-fg">{c.name}</p>
                    <p className="line-clamp-2 text-caption text-fg-muted">{c.description}</p>
                    <p className="text-caption text-fg-muted">{t('detail.refs', { n: c.ref_asset_ids.length })}</p>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </TabPanel>
      </Tabs>

      <ProjectForm open={editing} onOpenChange={setEditing} project={p} onSaved={() => project.refetch()} />
      <DeleteProject project={deleting ? p : null} onOpenChange={setDeleting} onDeleted={() => navigate('/projects')} />
    </div>
  )
}
