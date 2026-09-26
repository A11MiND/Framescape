import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckSquare, Download, FolderInput, ImageIcon, MoreHorizontal, Search, Trash2, Upload, X } from 'lucide-react'
import { Button, ConfirmDialog, Dialog, EmptyState, ErrorState, Field, IconButton, Input, Menu, PageHeader, Select, Skeleton, TabList, TabPanel, Tabs } from '../../ui'
import { assetsApi, type AssetFilter, type AssetInfo } from '../../lib/api/assets'
import { createApi } from '../../lib/api/create'
import { projectsApi } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { useDebouncedValue } from '../../hooks/useDebouncedValue'
import { useToast } from '../../components/Toast'
import { AssetCard } from './AssetCard'
import { assetTitle } from './assetTitle'
import { UploadDrawer } from './UploadDrawer'
import { TrashView } from './TrashView'

function saveBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** The asset library (spec P12): browse, select, move, delete and upload. */
export default function LibraryPage() {
  const { t } = useTranslation('library')
  const qc = useQueryClient()
  const toast = useToast()
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') === 'trash' ? 'trash' : 'mine'
  const type = params.get('type') ?? ''
  const project = params.get('project') ?? ''
  const [search, setSearch] = useState(params.get('q') ?? '')
  const q = useDebouncedValue(search.trim(), 300)
  const filter: AssetFilter = { ...(type ? { type } : {}), ...(project ? { project_id: project } : {}), ...(q ? { q } : {}) }
  const setParam = (key: string, value: string) => {
    const next = new URLSearchParams(params)
    if (value) next.set(key, value)
    else next.delete(key)
    setParams(next, { replace: true })
  }

  const caps = useQuery({ queryKey: keys.capabilities, queryFn: createApi.capabilities, staleTime: 60_000 })
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list })
  const trash = useQuery({ queryKey: [...keys.assets.all, 'trash'], queryFn: assetsApi.trash })
  const list = useInfiniteQuery({
    queryKey: [...keys.assets.all, 'list', filter],
    queryFn: ({ pageParam }) => assetsApi.list(filter, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
  })
  const assets = useMemo(() => list.data?.pages.flatMap((p) => p.assets) ?? [], [list.data])
  const projectName = useMemo(() => new Map((projects.data?.projects ?? []).map((p) => [p.biz_id, p.name])), [projects.data])

  const [selecting, setSelecting] = useState(false)
  const [selected, setSelected] = useState<Map<string, AssetInfo>>(new Map())
  const visible = new Set(assets.map((a) => a.biz_id))
  const hidden = [...selected.keys()].filter((id) => !visible.has(id)).length
  const toggle = (a: AssetInfo) =>
    setSelected((cur) => {
      const next = new Map(cur)
      if (next.has(a.biz_id)) next.delete(a.biz_id)
      else next.set(a.biz_id, a)
      return next
    })
  const clearSelection = () => setSelected(new Map())
  const clearHidden = () => setSelected((cur) => new Map([...cur].filter(([id]) => visible.has(id))))

  const [uploading, setUploading] = useState(false)
  const [moving, setMoving] = useState<string[] | null>(null)
  const [moveTarget, setMoveTarget] = useState('')
  const [deleting, setDeleting] = useState<string[] | null>(null)
  const retention = caps.data?.assets?.trash_retention_days ?? 30
  const uploadLimits = {
    image: caps.data?.uploads?.image.max_bytes ?? 20 << 20,
    video: caps.data?.uploads?.video.max_bytes ?? 512 << 20,
    audio: caps.data?.uploads?.audio.max_bytes ?? 50 << 20,
  }
  const refresh = () => qc.invalidateQueries({ queryKey: keys.assets.all })

  const restore = useMutation({
    mutationFn: (ids: string[]) => assetsApi.batch('restore', ids),
    onSuccess: (res) => {
      toast(t('done.restored', { n: res.affected }))
      refresh()
    },
    onError: (err) => toast(errorText(t, err)),
  })
  const remove = useMutation({
    mutationFn: (ids: string[]) => assetsApi.batch('delete', ids),
    onSuccess: (res, ids) => {
      setDeleting(null)
      setSelected((cur) => new Map([...cur].filter(([id]) => !ids.includes(id))))
      toast(t('done.deleted', { n: res.affected }), { label: t('done.undo'), onClick: () => restore.mutate(ids) })
      refresh()
    },
    onError: (err) => toast(errorText(t, err)),
  })
  const move = useMutation({
    mutationFn: ({ ids, target }: { ids: string[]; target: string }) => assetsApi.batch('move', ids, target),
    onSuccess: (res) => {
      setMoving(null)
      toast(t('done.moved', { n: res.affected }))
      refresh()
    },
    onError: (err) => toast(errorText(t, err)),
  })
  const download = useMutation({
    mutationFn: (ids: string[]) => {
      toast(t('done.downloading', { n: ids.length }))
      return assetsApi.download(ids)
    },
    onSuccess: (blob) => saveBlob(blob, 'assets.zip'),
    onError: (err) => toast(errorText(t, err)),
  })

  const ids = [...selected.keys()]
  const hiddenOf = (targets: string[]) => targets.filter((id) => !visible.has(id)).length
  const filtered = Boolean(type || project || q)

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6">
      <PageHeader
        title={t('title')}
        description={t('description')}
        actions={
          <Button variant="primary" icon={<Upload aria-hidden className="size-4" />} onClick={() => setUploading(true)}>
            {t('upload')}
          </Button>
        }
      />
      <Tabs value={tab} onValueChange={(v) => setParam('tab', v === 'trash' ? 'trash' : '')}>
        <TabList
          label={t('tab.label')}
          items={[
            { value: 'mine', label: t('tab.mine') },
            { value: 'trash', label: t('tab.trash'), ...(trash.data ? { count: trash.data.total } : {}) },
          ]}
        />
        <TabPanel value="mine" className="flex flex-col gap-4 pt-4">
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative min-w-[220px] flex-1">
              <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-fg-muted" />
              <Input
                value={search}
                aria-label={t('filter.search')}
                placeholder={t('filter.search')}
                onChange={(e) => {
                  setSearch(e.target.value)
                  setParam('q', e.target.value.trim())
                }}
                className="pl-9"
              />
            </div>
            <Select aria-label={t('filter.type')} value={type} onChange={(e) => setParam('type', e.target.value)}>
              <option value="">{t('filter.allTypes')}</option>
              <option value="image">{t('filter.image')}</option>
              <option value="video">{t('filter.video')}</option>
            </Select>
            <Select aria-label={t('filter.project')} value={project} onChange={(e) => setParam('project', e.target.value)}>
              <option value="">{t('filter.allProjects')}</option>
              {(projects.data?.projects ?? []).map((p) => (
                <option key={p.biz_id} value={p.biz_id}>
                  {p.name}
                </option>
              ))}
            </Select>
            <Button
              icon={<CheckSquare aria-hidden className="size-4" />}
              aria-pressed={selecting}
              onClick={() => {
                if (selecting) clearSelection()
                setSelecting(!selecting)
              }}
            >
              {selecting ? t('select.done') : t('select.start')}
            </Button>
          </div>

          {list.isPending ? (
            <ul className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3 sm:grid-cols-[repeat(auto-fill,minmax(220px,1fr))]">
              {Array.from({ length: 8 }, (_, i) => (
                <Skeleton key={i} className="aspect-[4/3.8] w-full" />
              ))}
            </ul>
          ) : list.isError ? (
            <ErrorState message={errorText(t, list.error)} onRetry={() => list.refetch()} />
          ) : assets.length === 0 ? (
            filtered ? (
              <EmptyState
                icon={<ImageIcon className="size-7" />}
                title={t('empty.filtered')}
                action={
                  <Button
                    onClick={() => {
                      setSearch('')
                      setParams(new URLSearchParams(), { replace: true })
                    }}
                  >
                    {t('empty.clear')}
                  </Button>
                }
              />
            ) : (
              <EmptyState
                icon={<ImageIcon className="size-7" />}
                title={t('empty.title')}
                body={t('empty.body')}
                action={
                  <Button variant="primary" onClick={() => setUploading(true)}>
                    {t('upload')}
                  </Button>
                }
              />
            )
          ) : (
            <>
              <ul className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3 sm:grid-cols-[repeat(auto-fill,minmax(220px,1fr))]">
                {assets.map((a) => (
                  <AssetCard
                    key={a.biz_id}
                    asset={a}
                    projectName={projectName.get(a.project_id)}
                    selecting={selecting}
                    selected={selected.has(a.biz_id)}
                    onToggle={() => toggle(a)}
                    menu={
                      !selecting && (
                        <Menu
                          trigger={<IconButton size="sm" label={t('card.more', { name: assetTitle(t, a) })} icon={<MoreHorizontal className="size-4" />} />}
                          items={[
                            { key: 'download', label: t('card.download'), icon: <Download className="size-4" />, onSelect: () => download.mutate([a.biz_id]) },
                            { key: 'move', label: t('card.move'), icon: <FolderInput className="size-4" />, onSelect: () => (setMoveTarget(a.project_id), setMoving([a.biz_id])) },
                            { key: 'delete', label: t('card.delete'), icon: <Trash2 className="size-4" />, danger: true, onSelect: () => setDeleting([a.biz_id]) },
                          ]}
                        />
                      )
                    }
                  />
                ))}
              </ul>
              {list.hasNextPage && (
                <div className="flex justify-center">
                  <Button loading={list.isFetchingNextPage} onClick={() => list.fetchNextPage()}>
                    {t('loadMore')}
                  </Button>
                </div>
              )}
            </>
          )}

          {selected.size > 0 && (
            <div role="region" aria-label={t('select.count', { n: selected.size })} className="sticky bottom-3 z-20 flex flex-wrap items-center gap-2 rounded-card border border-border bg-surface p-3 shadow-overlay">
              <span className="text-body font-semibold text-fg tabular-nums">{t('select.count', { n: selected.size })}</span>
              {hidden > 0 && (
                <span className="flex items-center gap-1 text-caption text-warning-fg">
                  {t('select.hidden', { n: hidden })}
                  <Button size="sm" variant="ghost" onClick={clearHidden}>
                    {t('select.clearHidden')}
                  </Button>
                </span>
              )}
              <div className="ml-auto flex flex-wrap gap-2">
                <Button size="sm" icon={<Download aria-hidden className="size-4" />} loading={download.isPending} onClick={() => download.mutate(ids)}>
                  {t('select.download')}
                </Button>
                <Button size="sm" icon={<FolderInput aria-hidden className="size-4" />} onClick={() => (setMoveTarget(''), setMoving(ids))}>
                  {t('select.move')}
                </Button>
                <Button size="sm" variant="danger-outline" icon={<Trash2 aria-hidden className="size-4" />} onClick={() => setDeleting(ids)}>
                  {t('select.delete')}
                </Button>
                <Button size="sm" variant="ghost" icon={<X aria-hidden className="size-4" />} onClick={clearSelection}>
                  {t('select.clear')}
                </Button>
              </div>
            </div>
          )}
        </TabPanel>
        <TabPanel value="trash" className="pt-4">
          <TrashView retention={retention} />
        </TabPanel>
      </Tabs>

      <UploadDrawer open={uploading} onOpenChange={setUploading} limits={uploadLimits} />

      <Dialog
        open={moving !== null}
        onOpenChange={(o) => !o && setMoving(null)}
        title={t('move.title')}
        locked={move.isPending}
        footer={
          <>
            <Button onClick={() => setMoving(null)} disabled={move.isPending}>
              {t('ui:action.cancel')}
            </Button>
            <Button variant="primary" loading={move.isPending} onClick={() => moving && move.mutate({ ids: moving, target: moveTarget })}>
              {t('move.confirm')}
            </Button>
          </>
        }
      >
        {moving && (
          <div className="flex flex-col gap-3 text-body text-fg">
            <p>{t('move.body', { n: moving.length })}</p>
            {hiddenOf(moving) > 0 && <p className="text-caption text-warning-fg">{t('move.hidden', { n: hiddenOf(moving) })}</p>}
            <Field label={t('move.target')}>
              <Select value={moveTarget} onChange={(e) => setMoveTarget(e.target.value)}>
                <option value="">{t('move.none')}</option>
                {(projects.data?.projects ?? []).map((p) => (
                  <option key={p.biz_id} value={p.biz_id}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
        )}
      </Dialog>

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={t('delete.title', { n: deleting?.length ?? 0 })}
        body={
          <>
            <p>{t('delete.body', { days: retention })}</p>
            {deleting && hiddenOf(deleting) > 0 && <p className="mt-1 text-caption text-warning-fg">{t('delete.hidden', { n: hiddenOf(deleting) })}</p>}
          </>
        }
        confirmLabel={t('delete.confirm')}
        danger
        busy={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </div>
  )
}
