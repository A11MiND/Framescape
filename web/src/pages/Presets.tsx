import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, presetDisplayName, type Preset } from '../lib/api'
import AppShell from '../components/AppShell'

// F4.3: presets are seed data (style/pose/composition/lighting/camera) — no
// create endpoint exists, this is a browse/reference view for what the
// preset chips in the creation studio actually mean.
export default function Presets() {
  const { t, i18n } = useTranslation()
  const presets = useQuery({ queryKey: ['presets'], queryFn: () => api.listPresets() })

  const grouped = (presets.data?.presets ?? []).reduce<Record<string, Preset[]>>((acc, p) => {
    ;(acc[p.category] ??= []).push(p)
    return acc
  }, {})

  return (
    <AppShell>
      <div className="mx-auto max-w-5xl space-y-8 px-6 py-8">
        <h1 className="text-lg font-medium">{t('presets.title')}</h1>

        {Object.entries(grouped).map(([category, items]) => (
          <section key={category}>
            <p className="mb-3 text-xs uppercase tracking-wide text-zinc-500">
              {t(`presets.category.${category}`, category)}
            </p>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {items.map((p) => (
                <div
                  key={p.biz_id}
                  className="overflow-hidden rounded-2xl border border-zinc-800 bg-zinc-900 transition hover:border-zinc-700"
                >
                  {p.cover_url ? (
                    <img src={p.cover_url} alt="" className="aspect-square w-full object-cover" />
                  ) : (
                    <div className="aspect-square w-full bg-zinc-800" />
                  )}
                  <div className="p-4">
                    <div className="flex items-center gap-2">
                      <p className="truncate font-medium">{presetDisplayName(p, i18n.language)}</p>
                      {p.style_type && p.style_type !== p.name && (
                        <span className="shrink-0 rounded-full border border-zinc-700 px-1.5 py-0.5 text-[10px] text-zinc-500">
                          {p.style_type}
                        </span>
                      )}
                    </div>
                    <p className="mt-1 text-xs leading-relaxed text-zinc-500">{p.prompt_fragment}</p>
                  </div>
                </div>
              ))}
            </div>
          </section>
        ))}
      </div>
    </AppShell>
  )
}
