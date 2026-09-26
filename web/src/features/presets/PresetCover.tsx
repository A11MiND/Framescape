import { useTranslation } from 'react-i18next'
import { Aperture, Frame, Lightbulb, Palette, PersonStanding } from 'lucide-react'
import type { Preset } from '../../lib/api/presets'

const ICONS: Record<string, typeof Palette> = { style: Palette, pose: PersonStanding, composition: Frame, lighting: Lightbulb, camera: Aperture }

/** A preset's cover, or a neutral mark of its category when it has none. */
export function PresetCover({ preset, className }: { preset: Preset; className: string }) {
  const { t } = useTranslation('presets')
  const Icon = ICONS[preset.category] ?? Palette
  return (
    <span className={`flex items-center justify-center overflow-hidden bg-surface-2 text-fg-muted ${className}`}>
      {preset.cover_url ? (
        <img src={preset.cover_url} alt="" loading="lazy" className="size-full object-cover" />
      ) : (
        <span className="flex flex-col items-center gap-1 text-caption">
          <Icon aria-hidden className="size-7" />
          {t('noCover')}
        </span>
      )}
    </span>
  )
}
