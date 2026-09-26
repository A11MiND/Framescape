import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { FolderOpen, MoreHorizontal, Pencil, Plus, Search, Trash2 } from 'lucide-react'
import { Button, Card, EmptyState, ErrorState, IconButton, Input, Menu, PageHeader, Skeleton } from '../../ui'
import { projectsApi, type ProjectSummary } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { formatDate } from '../../lib/format'
import { useDebouncedValue } from '../../hooks/useDebouncedValue'
import { DeleteProject, ProjectForm } from './ProjectDialogs'

export function ProjectCover({ project, className }: { project: ProjectSummary; className?: string }) {
  const { t } = useTranslation('projects')
  const cover = project.cover_urls[0]
  return (
    <span className={`flex items-center justify-center overflow-hidden bg-surface-2 ${className ?? ''}`}>
      {cover ? (
        <img src={cover} alt="" loading="lazy" className="size-full object-cover" />
      ) : (
        <span className="flex flex-col items-center gap-1 text-caption text-fg-muted">
          <FolderOpen aria-hidden className="size-6" />
          {t('noCover')}
        </span>
      )}
    </span>
  )
}

/** Projects (spec P14): cards with real counts; each opens its own page. */
export default function ProjectsPage() {
  const { t, i18n } = useTranslation('projects')
  const [search, setSearch] = useState('')
  const q = useDebouncedValue(search.trim(), 300)
  const list = useQuery({ queryKey: [...keys.projects.all, 'search', q], queryFn: () => projectsApi.search(q) })
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<ProjectSummary | null>(null)
  const [deleting, setDeleting] = useState<ProjectSummary | null>(null)
  const projects = list.data?.projects ?? []

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6">
      <PageHeader
        title={t('title')}
        description={t('description')}
        actions={
          <Button variant="primary" icon={<Plus aria-hidden className="size-4" />} onClick={() => setCreating(true)}>
            {t('create')}
          </Button>
        }
      />
      <div className="relative max-w-md">
        <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-fg-muted" />
        <Input value={search} aria-label={t('search')} placeholder={t('search')} onChange={(e) => setSearch(e.target.value)} className="pl-9" />
      </div>
      {list.isPending ? (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-4">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-64 w-full" />
          ))}
        </div>
      ) : list.isError ? (
        <ErrorState message={errorText(t, list.error)} onRetry={() => list.refetch()} />
      ) : projects.length === 0 ? (
        <Card padding="none">
          {q ? (
            <EmptyState icon={<FolderOpen className="size-7" />} title={t('empty.filtered')} />
          ) : (
            <EmptyState
              icon={<FolderOpen className="size-7" />}
              title={t('empty.title')}
              body={t('empty.body')}
              action={
                <Button variant="primary" onClick={() => setCreating(true)}>
                  {t('create')}
                </Button>
              }
            />
          )}
        </Card>
      ) : (
        <ul className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-4">
          {projects.map((p) => (
            <li key={p.biz_id} className="flex flex-col overflow-hidden rounded-card border border-border bg-surface">
              <Link to={`/projects/${p.biz_id}`} aria-label={p.name} className="block">
                <ProjectCover project={p} className="aspect-[16/9] w-full" />
              </Link>
              <div className="flex items-start gap-1 p-3">
                <div className="min-w-0 flex-1">
                  <Link to={`/projects/${p.biz_id}`} className="block truncate text-body font-semibold text-fg hover:underline">
                    {p.name}
                  </Link>
                  <p className="line-clamp-2 min-h-10 text-caption text-fg-muted">{p.description || t('noDescription')}</p>
                  <p className="mt-2 text-caption text-fg tabular-nums">{t('stats', { assets: p.asset_count, jobs: p.job_count, characters: p.character_count })}</p>
                  <p className="text-caption text-fg-muted">
                    {p.last_activity_at ? t('updated', { time: formatDate(p.last_activity_at, i18n.language) }) : t('created', { time: formatDate(p.created_at, i18n.language) })}
                  </p>
                </div>
                <Menu
                  trigger={<IconButton size="sm" label={t('more', { name: p.name })} icon={<MoreHorizontal className="size-4" />} />}
                  items={[
                    { key: 'edit', label: t('edit'), icon: <Pencil className="size-4" />, onSelect: () => setEditing(p) },
                    { key: 'delete', label: t('delete'), icon: <Trash2 className="size-4" />, danger: true, onSelect: () => setDeleting(p) },
                  ]}
                />
              </div>
            </li>
          ))}
        </ul>
      )}
      <ProjectForm open={creating} onOpenChange={setCreating} />
      <ProjectForm open={editing !== null} onOpenChange={(o) => !o && setEditing(null)} project={editing ?? undefined} />
      <DeleteProject project={deleting} onOpenChange={(o) => !o && setDeleting(null)} />
    </div>
  )
}
