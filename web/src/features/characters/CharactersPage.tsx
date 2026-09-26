import { useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FolderOpen, MoreHorizontal, Pencil, Plus, Search, Sparkles, Trash2, Upload, UserRound } from 'lucide-react'
import { Button, Card, ConfirmDialog, EmptyState, ErrorState, IconButton, Input, Menu, PageHeader, Skeleton } from '../../ui'
import { charactersApi, type Character } from '../../lib/api/characters'
import { assetsApi } from '../../lib/api/assets'
import { projectsApi } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { uploadAsset } from '../../lib/upload'
import { lastCreateMode } from '../../app/routing'
import { useToast } from '../../components/Toast'
import { CharacterDrawer } from './CharacterDrawer'
import { useCharacterLimits } from './limits'

function RefImage({ id, className }: { id?: string; className: string }) {
  const asset = useQuery({ queryKey: keys.assets.detail(id ?? ''), queryFn: () => assetsApi.get(id!), enabled: Boolean(id) })
  return (
    <span className={`flex items-center justify-center overflow-hidden bg-surface-2 text-fg-muted ${className}`}>
      {asset.data ? <img src={asset.data.thumb_url || asset.data.public_url} alt="" loading="lazy" className="size-full object-cover" /> : <UserRound aria-hidden className="size-6" />}
    </span>
  )
}

/** The mode "use in creation" opens: the last one used, unless it has no character binding. */
function creationModeFor() {
  const mode = lastCreateMode()
  return mode === 'comic' ? 'image' : mode
}

/** Character library (spec P15): reference images and a look, bound when creating. */
export default function CharactersPage() {
  const { t } = useTranslation('characters')
  const qc = useQueryClient()
  const toast = useToast()
  const location = useLocation()
  const navigate = useNavigate()
  const { maxRefs } = useCharacterLimits()
  const list = useQuery({ queryKey: keys.characters, queryFn: charactersApi.list })
  const projects = useQuery({ queryKey: keys.projects.list(), queryFn: projectsApi.list })
  const projectName = new Map((projects.data?.projects ?? []).map((p) => [p.biz_id, p.name]))
  const [search, setSearch] = useState('')
  const [drawer, setDrawer] = useState<{ character?: Character; refs?: string[] } | null>(null)
  const [deleting, setDeleting] = useState<Character | null>(null)
  const [uploading, setUploading] = useState(false)
  const input = useRef<HTMLInputElement>(null)

  // "Save as character" from a result or an asset opens the form with that image.
  useEffect(() => {
    const id = (location.state as { prefillAssetId?: string } | null)?.prefillAssetId
    if (!id) return
    setDrawer({ refs: [id] })
    navigate('.', { replace: true, state: {} })
  }, [location.state, navigate])

  const remove = useMutation({
    mutationFn: (id: string) => charactersApi.remove(id),
    onSuccess: () => {
      toast(t('deleteDialog.done'))
      setDeleting(null)
      qc.invalidateQueries({ queryKey: keys.characters })
      qc.invalidateQueries({ queryKey: keys.projects.all })
    },
    onError: (err) => toast(errorText(t, err)),
  })

  const uploadFirst = async (files: FileList | null) => {
    if (!files?.length) return
    setUploading(true)
    try {
      const ids: string[] = []
      for (const file of [...files].filter((f) => f.type.startsWith('image/')).slice(0, maxRefs)) ids.push((await uploadAsset(file)).biz_id)
      if (ids.length) setDrawer({ refs: ids })
    } catch (err) {
      toast(errorText(t, err))
    } finally {
      setUploading(false)
    }
  }

  const q = search.trim().toLowerCase()
  const all = list.data?.characters ?? []
  const shown = q ? all.filter((c) => c.name.toLowerCase().includes(q) || c.description.toLowerCase().includes(q)) : all
  const use = (c: Character) => navigate(`/create/${creationModeFor()}`, { state: { prefillCharacterId: c.biz_id } })

  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6">
      <PageHeader
        title={t('title')}
        description={t('description')}
        actions={
          <Button variant="primary" icon={<Plus aria-hidden className="size-4" />} onClick={() => setDrawer({})}>
            {t('create')}
          </Button>
        }
      />
      {all.length > 0 && (
        <div className="relative max-w-md">
          <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-fg-muted" />
          <Input value={search} aria-label={t('search')} placeholder={t('search')} onChange={(e) => setSearch(e.target.value)} className="pl-9" />
        </div>
      )}
      <input ref={input} type="file" accept="image/png,image/jpeg,image/webp" multiple className="sr-only" tabIndex={-1} aria-label={t('empty.upload')} onChange={(e) => {
          void uploadFirst(e.target.files)
          e.target.value = ''
        }}
      />

      {list.isPending ? (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-4">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-96 w-full" />
          ))}
        </div>
      ) : list.isError ? (
        <ErrorState message={errorText(t, list.error)} onRetry={() => list.refetch()} />
      ) : all.length === 0 ? (
        <Card padding="none">
          <EmptyState
            icon={<UserRound className="size-7" />}
            title={t('empty.title')}
            body={t('empty.body', { max: maxRefs })}
            action={
              <Button variant="primary" icon={<Upload aria-hidden className="size-4" />} loading={uploading} onClick={() => input.current?.click()}>
                {uploading ? t('empty.uploading') : t('empty.upload')}
              </Button>
            }
          />
        </Card>
      ) : shown.length === 0 ? (
        <Card padding="none">
          <EmptyState icon={<Search className="size-7" />} title={t('empty.filtered')} />
        </Card>
      ) : (
        <ul className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-4">
          {shown.map((c) => (
            <li key={c.biz_id} className="flex flex-col overflow-hidden rounded-card border border-border bg-surface">
              <div className="relative">
                <RefImage id={c.ref_asset_ids[0]} className="aspect-[4/3] w-full" />
                <div className="absolute top-2 right-2">
                  <Menu
                    trigger={<IconButton size="sm" label={t('more', { name: c.name })} icon={<MoreHorizontal className="size-4" />} variant="secondary" />}
                    items={[
                      { key: 'edit', label: t('edit'), icon: <Pencil className="size-4" />, onSelect: () => setDrawer({ character: c }) },
                      { key: 'delete', label: t('delete'), icon: <Trash2 className="size-4" />, danger: true, onSelect: () => setDeleting(c) },
                    ]}
                  />
                </div>
              </div>
              {c.ref_asset_ids.length > 1 && (
                <div className="flex gap-2 px-3 pt-3">
                  {c.ref_asset_ids.slice(1, 3).map((id) => (
                    <RefImage key={id} id={id} className="size-14 rounded-thumb" />
                  ))}
                </div>
              )}
              <div className="flex flex-1 flex-col gap-1 p-3">
                <h2 className="truncate text-body font-semibold text-fg">{c.name}</h2>
                <p className="line-clamp-3 min-h-[3lh] text-caption text-fg-muted">{c.description || t('noDescription')}</p>
                <p className="mb-2 flex items-center gap-1.5 text-caption text-fg-muted">
                  <FolderOpen aria-hidden className="size-3.5 shrink-0" />
                  <span className="truncate">{(c.project_id && projectName.get(c.project_id)) || t('noProject')}</span>
                  <span aria-hidden>·</span>
                  <span className="shrink-0">{t('refs', { n: c.ref_asset_ids.length })}</span>
                </p>
                <Button variant="primary" className="mt-auto w-full" icon={<Sparkles aria-hidden className="size-4" />} title={t('useHint', { name: c.name })} onClick={() => use(c)}>
                  {t('use')}
                </Button>
              </div>
            </li>
          ))}
          {!q && (
            <li>
              <button
                type="button"
                onClick={() => setDrawer({})}
                className="flex size-full min-h-64 flex-col items-center justify-center gap-2 rounded-card border-2 border-dashed border-border-control p-4 text-center text-fg-muted hover:bg-surface-2 hover:text-fg"
              >
                <Plus aria-hidden className="size-7 text-primary-text" />
                <span className="text-body font-medium text-primary-text">{t('createTile')}</span>
                <span className="text-caption">{t('createTileHint', { max: maxRefs })}</span>
              </button>
            </li>
          )}
        </ul>
      )}

      <CharacterDrawer open={drawer !== null} onOpenChange={(o) => !o && setDrawer(null)} character={drawer?.character} initialRefs={drawer?.refs} />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={deleting ? t('deleteDialog.title', { name: deleting.name }) : ''}
        body={t('deleteDialog.body')}
        confirmLabel={t('deleteDialog.confirm')}
        danger
        busy={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting.biz_id)}
      />
    </div>
  )
}
