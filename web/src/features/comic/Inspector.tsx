import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, Eye, EyeOff, Image as ImageIcon, Lock, LockOpen, Trash2, ArrowUpToLine } from 'lucide-react'
import { Button, Field, IconButton, Input, Select, TabList, TabPanel, Tabs, Textarea, cn } from '../../ui'
import { constrainLayer, type ComicDocument, type ComicLayer } from '../../lib/comicDocument'

interface Props {
  doc: ComicDocument
  selected: string | null
  tab: 'layers' | 'properties'
  onTab: (tab: 'layers' | 'properties') => void
  onSelect: (id: string) => void
  onLayer: (layer: ComicLayer) => void
  onDocument: (doc: ComicDocument) => void
  onRemove: (id: string) => void
  overflow: string[]
  disabled?: boolean
}

function layerName(t: (k: string) => string, l: ComicLayer) {
  if (l.kind === 'logo') return t('art.logo')
  return l.text.trim().slice(0, 24) || t('art.emptyText')
}

/** Keep partial input local; commit only when the user finishes typing. */
function NumericProperty({ value, min, max, disabled, onCommit }: { value: number; min: number; max: number; disabled?: boolean; onCommit: (value: number) => void }) {
  const [draft, setDraft] = useState<string | null>(null)
  const commit = () => {
    const parsed = draft === null || draft.trim() === '' ? value : Number(draft)
    const next = Number.isFinite(parsed) ? Math.max(min, Math.min(max, Math.round(parsed))) : value
    setDraft(null)
    if (next !== value) onCommit(next)
  }
  return <Input type="number" min={min} max={max} step={1} disabled={disabled} value={draft ?? value}
    onChange={(e) => setDraft(e.target.value)} onBlur={commit}
    onKeyDown={(e) => {
      if (e.key === 'Enter') { e.preventDefault(); e.currentTarget.blur() }
      if (e.key === 'Escape') { e.preventDefault(); setDraft(null) }
    }} />
}

/** Layers (visibility, lock, order) and the selected layer's properties. */
export function Inspector({ doc, selected, tab, onTab, onSelect, onLayer, onDocument, onRemove, overflow, disabled }: Props) {
  const { t } = useTranslation('comic')
  const layer = doc.layers.find((l) => l.id === selected)
  const pageLabel = !doc.page_asset_id ? t('inspector.pageEmpty') : doc.page_source === 'imported' ? t('inspector.pageImported') : doc.page_source === 'generated' ? t('inspector.pageGenerated') : t('inspector.pageSaved')

  return (
    <Tabs value={tab} onValueChange={(v) => onTab(v as 'layers' | 'properties')}>
      <TabList
        label={t('inspector.label')}
        items={[
          { value: 'layers', label: t('inspector.layers'), count: doc.layers.length },
          { value: 'properties', label: t('inspector.properties') },
        ]}
      />
      <TabPanel value="layers" className="pt-3">
        <ul className="flex flex-col gap-1" aria-label={t('art.layers')}>
          {[...doc.layers].reverse().map((l) => (
            <li key={l.id} className={cn('flex items-center gap-1 rounded-card px-1.5', selected === l.id ? 'bg-primary-soft' : 'hover:bg-surface-2')}>
              <button type="button" aria-pressed={selected === l.id} onClick={() => onSelect(l.id)} className={cn('min-w-0 flex-1 truncate py-2 text-left text-body', l.hidden ? 'text-fg-muted line-through' : 'text-fg')}>
                {layerName(t, l)}
                {overflow.includes(l.id) && <span className="ml-1.5 text-caption text-warning-fg">{t('inspector.overflowMark')}</span>}
              </button>
              <IconButton
                size="sm"
                label={l.hidden ? t('inspector.show', { name: layerName(t, l) }) : t('inspector.hide', { name: layerName(t, l) })}
                icon={l.hidden ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                disabled={disabled}
                onClick={() => onLayer({ ...l, hidden: !l.hidden })}
              />
              <IconButton
                size="sm"
                label={l.locked ? t('inspector.unlockLayer', { name: layerName(t, l) }) : t('inspector.lockLayer', { name: layerName(t, l) })}
                icon={l.locked ? <Lock className="size-4" /> : <LockOpen className="size-4" />}
                disabled={disabled}
                onClick={() => onLayer({ ...l, locked: !l.locked })}
              />
            </li>
          ))}
          <li className="flex items-center gap-2 rounded-card bg-surface-2 px-2 py-2 text-body text-fg-muted">
            <ImageIcon aria-hidden className="size-4 shrink-0" />
            <span className="min-w-0 flex-1 truncate">{pageLabel}</span>
            <span className="shrink-0 rounded-badge border border-border px-1.5 text-badge">{t('inspector.notSplittable')}</span>
          </li>
        </ul>
        {!doc.layers.length && <p className="mt-2 text-caption text-fg-muted">{t('art.layersEmpty')}</p>}
        <p className="mt-2 text-caption text-fg-muted">{t('inspector.baseHint')}</p>
      </TabPanel>
      <TabPanel value="properties" className="pt-3">
        {!layer ? (
          <p className="text-caption text-fg-muted">{t('art.pick')}</p>
        ) : (
          <div className="flex flex-col gap-3">
            {layer.kind !== 'logo' && (
              <>
                <Field label={t('art.text')}>
                  <Textarea maxChars={2000} disabled={layer.locked || disabled} value={layer.text} onChange={(e) => onLayer({ ...layer, text: e.target.value })} className="min-h-24" />
                </Field>
                {overflow.includes(layer.id) && (
                  <p role="alert" className="text-caption text-warning-fg">
                    {t('inspector.overflow')}
                  </p>
                )}
                <div className="grid grid-cols-3 gap-2 [&>*]:min-w-0">
                  <Field label={t('art.fontSize')}>
                    <NumericProperty key={`${layer.id}-font`} min={12} max={96}
                      disabled={layer.locked || disabled} value={layer.font_size}
                      onCommit={(font_size) => onLayer({ ...layer, font_size })} />
                  </Field>
                  <Field label={t('art.color')}>
                    <input type="color" disabled={layer.locked || disabled} value={layer.color} onChange={(e) => onLayer({ ...layer, color: e.target.value })} className="h-10 w-full rounded-card border border-border-control bg-surface p-1" />
                  </Field>
                  <Field label={t('art.fill')}>
                    <input type="color" disabled={layer.locked || disabled} value={layer.fill} onChange={(e) => onLayer({ ...layer, fill: e.target.value })} className="h-10 w-full rounded-card border border-border-control bg-surface p-1" />
                  </Field>
                </div>
                {layer.kind === 'bubble' && <Field label={t('art.opacity')} help={t('art.opacityHelp')}>
                  <NumericProperty key={`${layer.id}-opacity`} min={0} max={100}
                    value={Math.round((layer.fill_opacity ?? 1) * 100)} disabled={layer.locked || disabled}
                    onCommit={(value) => onLayer({ ...layer, fill_opacity: value / 100 })} />
                </Field>}
                {layer.kind === 'bubble' && (
                  <Field label={t('art.tail')}>
                    <Select disabled={layer.locked || disabled} value={layer.tail} onChange={(e) => onLayer({ ...layer, tail: e.target.value as ComicLayer['tail'] })}>
                      <option value="left">{t('art.tailLeft')}</option>
                      <option value="right">{t('art.tailRight')}</option>
                      <option value="none">{t('art.tailNone')}</option>
                    </Select>
                  </Field>
                )}
              </>
            )}
            <div className="grid grid-cols-2 gap-2 [&>*]:min-w-0">
              {(['x', 'y', 'w', 'h'] as const).map((key, i) => (
                <Field key={key} label={[t('art.left'), t('art.top'), t('art.width'), t('art.height')][i]}>
                  <NumericProperty key={`${layer.id}-${key}`} min={0} max={100}
                    value={Math.round(layer[key] * 100)} disabled={layer.locked || disabled}
                    onCommit={(value) => onLayer(constrainLayer({ ...layer, [key]: value / 100 }))} />
                </Field>
              ))}
            </div>
            <div className="flex flex-wrap gap-2">
              <Button size="sm" icon={layer.locked ? <LockOpen aria-hidden className="size-4" /> : <Lock aria-hidden className="size-4" />} disabled={disabled} onClick={() => onLayer({ ...layer, locked: !layer.locked })}>
                {layer.locked ? t('art.unlock') : t('art.lock')}
              </Button>
              <Button
                size="sm"
                icon={<ArrowUpToLine aria-hidden className="size-4" />}
                disabled={layer.locked || disabled}
                onClick={() => onDocument({ ...doc, layers: [...doc.layers.filter((l) => l.id !== layer.id), layer] })}
              >
                {t('art.toFront')}
              </Button>
              <Button
                size="sm"
                icon={<Copy aria-hidden className="size-4" />}
                disabled={layer.locked || disabled || doc.layers.length >= 64}
                onClick={() => {
                  const copy = constrainLayer({ ...layer, id: crypto.randomUUID(), x: layer.x + 0.02, y: layer.y + 0.02 })
                  onDocument({ ...doc, layers: [...doc.layers, copy] })
                  onSelect(copy.id)
                }}
              >
                {t('art.duplicate')}
              </Button>
              <Button size="sm" variant="danger-outline" icon={<Trash2 aria-hidden className="size-4" />} disabled={layer.locked || disabled} onClick={() => onRemove(layer.id)}>
                {t('art.delete')}
              </Button>
            </div>
          </div>
        )}
      </TabPanel>
    </Tabs>
  )
}
