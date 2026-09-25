import { useTranslation } from 'react-i18next'
import { CircleCheck, Download, FileJson, TriangleAlert } from 'lucide-react'
import { Button } from '../../ui'
import type { ComicDocument } from '../../lib/comicDocument'

interface Props {
  doc: ComicDocument
  overflow: string[]
  busy: boolean
  onFix: (layerId: string) => void
  onExport: () => void
  onBackup: () => void
}

/** Step 4: what still blocks the PNG, and the export itself. */
export function ExportPanel({ doc, overflow, busy, onFix, onExport, onBackup }: Props) {
  const { t } = useTranslation('comic')
  const hasPage = Boolean(doc.page_asset_id)
  const ready = hasPage && overflow.length === 0
  const name = (id: string) => doc.layers.find((l) => l.id === id)?.text.trim().slice(0, 20) || t('art.emptyText')
  return (
    <div className="flex flex-col gap-4">
      <ul className="flex flex-col gap-2 text-body">
        <li className="flex items-start gap-2">
          {hasPage ? <CircleCheck aria-hidden className="mt-0.5 size-4 shrink-0 text-success" /> : <TriangleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-warning" />}
          <span className={hasPage ? 'text-fg' : 'text-warning-fg'}>{hasPage ? t('export.pageReady') : t('export.pageMissing')}</span>
        </li>
        {overflow.length === 0 ? (
          <li className="flex items-start gap-2">
            <CircleCheck aria-hidden className="mt-0.5 size-4 shrink-0 text-success" />
            <span className="text-fg">{t('export.textFits')}</span>
          </li>
        ) : (
          overflow.map((id) => (
            <li key={id} className="flex flex-wrap items-center gap-2">
              <TriangleAlert aria-hidden className="size-4 shrink-0 text-warning" />
              <span className="min-w-0 flex-1 text-warning-fg">{t('export.overflow', { name: name(id) })}</span>
              <Button size="sm" onClick={() => onFix(id)}>
                {t('export.fix')}
              </Button>
            </li>
          ))
        )}
      </ul>
      <Button variant="primary" size="lg" icon={<Download aria-hidden className="size-5" />} loading={busy} disabled={!ready} onClick={onExport}>
        {t('toolbar.export')}
      </Button>
      <p className="text-caption text-fg-muted">{t('export.note')}</p>
      <Button icon={<FileJson aria-hidden className="size-4" />} onClick={onBackup}>
        {t('toolbar.backup')}
      </Button>
      <p className="text-caption text-fg-muted">{t('export.backupNote')}</p>
    </div>
  )
}
