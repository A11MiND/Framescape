import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Palette, Plus, Search, Sparkles, X } from 'lucide-react'
import { Button, Card, ConfirmDialog, Drawer, EmptyState, ErrorState, IconButton, Input, PageHeader, SegmentedControl, Skeleton, TabList, Tabs, cn } from '../../ui'
import { presetDisplayName } from '../../lib/api'
import { PRESET_CATEGORIES, presetsApi, type Preset } from '../../lib/api/presets'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { lastCreateMode } from '../../app/routing'
import { useMediaQuery } from '../../hooks/useMediaQuery'
import { useToast } from '../../components/Toast'
import { PresetCover } from './PresetCover'
import { PresetDetail } from './PresetDetail'

const PRESET_MODES = ['image', 'image-sequence', 'gpt', 'comic-classic']

/** The mode "apply to creation" opens: the last one used, if it takes presets. */
function creationModeFor() {
  const mode = lastCreateMode()
  return PRESET_MODES.includes(mode) ? mode : 'image'
}

/** Presets (spec P16): browse by category, select several, apply to creation. */
export default function PresetsPage() {
  const { t, i18n } = useTranslation('presets')
  const qc = useQueryClient()
  const toast = useToast()
  const navigate = useNavigate()
  const wide = useMediaQuery('(min-width: 1280px)')
  const [params, setParams] = useSearchParams()
  const category = (PRESET_CATEGORIES as readonly string[]).includes(params.get('category') ?? '') ? params.get('category')! : 'style'
  const source = params.get('source') === 'mine' ? 'mine' : 'system'
  const setParam = (key: string, value: string, fallback: string) => {
    const next = new URLSearchParams(params)
    if (value === fallback) next.delete(key)
    else next.set(key, value)
    setParams(next, { replace: true })
  }
  const list = useQuery({ queryKey: keys.presets, queryFn: presetsApi.list })
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  const [openId, setOpenId] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<Preset | null>(null)

  const all = list.data?.presets ?? []
  const byId = new Map(all.map((p) => [p.biz_id, p]))
  const name = (p: Preset) => presetDisplayName(p, i18n.language)
  const q = search.trim().toLowerCase()
  const shown = all.filter(
    (p) => p.category === category && (source === 'mine' ? p.mine : !p.mine) && (!q || name(p).toLowerCase().includes(q) || p.prompt_fragment.toLowerCase().includes(q)),
  )
  const open = openId ? (byId.get(openId) ?? null) : null
  const showDetail = creating || open !== null
  const chosen = selected.map((id) => byId.get(id)).filter((p): p is Preset => Boolean(p))
  const toggle = (id: string) => setSelected((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]))
  const closeDetail = () => {
    setOpenId(null)
    setCreating(false)
  }

  const apply = (presets: Preset[]) =>
    navigate(`/create/${creationModeFor()}`, { state: { prefillPresets: { ids: presets.map((p) => p.biz_id), names: presets.map(name) } } })

  const remove = useMutation({
    mutationFn: (id: string) => presetsApi.remove(id),
    onSuccess: (_, id) => {
      toast(t('deleteDialog.done'))
      setDeleting(null)
      setSelected((cur) => cur.filter((x) => x !== id))
      if (openId === id) setOpenId(null)
      qc.invalidateQueries({ queryKey: keys.presets })
    },
    onError: (err) => toast(errorText(t, err)),
  })

  const detail = (
    <PresetDetail
      preset={creating ? null : open}
      defaultCategory={category}
      onApply={(p) => apply([p])}
      onSaved={(p) => {
        setCreating(false)
        setOpenId(p.biz_id)
        const next = new URLSearchParams(params)
        next.set('source', 'mine')
        if (p.category === 'style') next.delete('category')
        else next.set('category', p.category)
        setParams(next, { replace: true })
      }}
      onDelete={setDeleting}
    />
  )

  return (
    <div className={cn('mx-auto flex max-w-[1440px] flex-col gap-5 px-4 py-6 lg:px-6', chosen.length > 0 && 'pb-24')}>
      <PageHeader
        title={t('title')}
        description={t('description')}
        actions={
          <Button
            variant="primary"
            icon={<Plus aria-hidden className="size-4" />}
            onClick={() => {
              setOpenId(null)
              setCreating(true)
            }}
          >
            {t('create')}
          </Button>
        }
      />
      <div className="flex flex-col gap-3">
        <Tabs value={category} onValueChange={(v) => setParam('category', v, 'style')}>
          <TabList label={t('categoryLabel')} items={PRESET_CATEGORIES.map((c) => ({ value: c, label: t(`create:presets.category.${c}`) }))} className="overflow-x-auto" />
        </Tabs>
        <div className="flex flex-wrap items-center gap-2">
          <SegmentedControl
            label={t('sourceLabel')}
            value={source}
            onChange={(v) => setParam('source', v, 'system')}
            options={[
              { value: 'system', label: t('source.system') },
              { value: 'mine', label: t('source.mine') },
            ]}
          />
          <div className="relative min-w-[220px] flex-1 sm:max-w-md">
            <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-fg-muted" />
            <Input value={search} aria-label={t('search')} placeholder={t('search')} onChange={(e) => setSearch(e.target.value)} className="pl-9" />
          </div>
        </div>
      </div>

      <div className={cn('grid items-start gap-5', showDetail && 'xl:grid-cols-[minmax(0,1fr)_360px]')}>
        {list.isPending ? (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(160px,1fr))] gap-3 sm:grid-cols-[repeat(auto-fill,minmax(220px,1fr))]">
            {Array.from({ length: 6 }, (_, i) => (
              <Skeleton key={i} className="aspect-[4/3] w-full" />
            ))}
          </div>
        ) : list.isError ? (
          <ErrorState message={errorText(t, list.error)} onRetry={() => list.refetch()} />
        ) : shown.length === 0 ? (
          <Card padding="none">
            {q ? (
              <EmptyState icon={<Search className="size-7" />} title={t('empty.filtered')} />
            ) : (
              <EmptyState
                icon={<Palette className="size-7" />}
                title={t('empty.mine')}
                body={t('empty.mineBody')}
                action={
                  <Button variant="primary" onClick={() => setCreating(true)}>
                    {t('create')}
                  </Button>
                }
              />
            )}
          </Card>
        ) : (
          <ul className="grid grid-cols-[repeat(auto-fill,minmax(160px,1fr))] gap-3 sm:grid-cols-[repeat(auto-fill,minmax(220px,1fr))]">
            {shown.map((p) => {
              const on = selected.includes(p.biz_id)
              return (
                <li key={p.biz_id} className={cn('relative overflow-hidden rounded-card border bg-surface', on ? 'border-primary ring-1 ring-primary' : openId === p.biz_id ? 'border-border-control' : 'border-border')}>
                  <button
                    type="button"
                    aria-label={t('open', { name: name(p) })}
                    onClick={() => {
                      setCreating(false)
                      setOpenId(p.biz_id)
                    }}
                    className="block w-full text-left"
                  >
                    <PresetCover preset={p} className="aspect-[4/3] w-full" />
                    <span className="block truncate px-3 py-2.5 text-body font-medium text-fg">{name(p)}</span>
                  </button>
                  <button
                    type="button"
                    aria-pressed={on}
                    aria-label={t('select', { name: name(p) })}
                    onClick={() => toggle(p.biz_id)}
                    className={cn(
                      'absolute top-2 left-2 inline-flex size-7 items-center justify-center rounded-full border-2',
                      on ? 'border-primary bg-primary text-white' : 'border-white bg-black/30 text-transparent hover:bg-black/45',
                    )}
                  >
                    <Check aria-hidden className="size-4" />
                  </button>
                </li>
              )
            })}
          </ul>
        )}
        {showDetail && wide && (
          <aside aria-label={creating ? t('detail.createTitle') : t('detail.title')} className="sticky top-4 max-h-[calc(100dvh-10rem)] overflow-y-auto rounded-card border border-border bg-surface p-4">
            <div className="mb-3 flex items-center justify-between">
              <h2 className="text-body font-semibold text-fg">{creating ? t('detail.createTitle') : t('detail.title')}</h2>
              <IconButton size="sm" label={t('detail.close')} icon={<X className="size-4" />} onClick={closeDetail} />
            </div>
            {detail}
          </aside>
        )}
      </div>
      {!wide && (
        <Drawer open={showDetail} onOpenChange={(o) => !o && closeDetail()} title={creating ? t('detail.createTitle') : t('detail.title')}>
          {detail}
        </Drawer>
      )}

      {chosen.length > 0 && (
        <div role="region" aria-label={t('tray.label')} className="sticky bottom-3 z-20 flex flex-wrap items-center gap-2 rounded-card border border-border bg-surface p-3 shadow-overlay">
          <span className="text-body font-semibold text-fg tabular-nums">{t('tray.count', { n: chosen.length })}</span>
          <ul className="flex min-w-0 flex-1 flex-wrap gap-1.5">
            {chosen.map((p) => (
              <li key={p.biz_id} className="inline-flex h-8 items-center gap-1 rounded-full border border-border bg-surface-2 pr-1 pl-3 text-caption text-fg">
                {name(p)}
                <button type="button" aria-label={t('tray.remove', { name: name(p) })} onClick={() => toggle(p.biz_id)} className="inline-flex size-6 items-center justify-center rounded-full text-fg-muted hover:bg-surface hover:text-fg">
                  <X aria-hidden className="size-3.5" />
                </button>
              </li>
            ))}
          </ul>
          <Button size="sm" variant="ghost" onClick={() => setSelected([])}>
            {t('tray.clear')}
          </Button>
          <Button size="sm" variant="primary" icon={<Sparkles aria-hidden className="size-4" />} title={t('tray.applyHint')} onClick={() => apply(chosen)}>
            {t('tray.apply')}
          </Button>
        </div>
      )}

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
