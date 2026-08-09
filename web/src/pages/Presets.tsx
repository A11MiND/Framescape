import { useQuery } from '@tanstack/react-query'
import { api, type Preset } from '../lib/api'
import Nav from '../components/Nav'

// F4.3: presets are seed data (style/pose/composition/lighting/camera) — no
// create endpoint exists, this is a browse/reference view for what the
// preset chips in the creation studio actually mean.
const CATEGORY_LABELS: Record<string, string> = {
  style: '风格',
  pose: '姿势',
  composition: '构图',
  lighting: '光线',
  camera: '镜头',
}

export default function Presets() {
  const presets = useQuery({ queryKey: ['presets'], queryFn: () => api.listPresets() })

  const grouped = (presets.data?.presets ?? []).reduce<Record<string, Preset[]>>((acc, p) => {
    ;(acc[p.category] ??= []).push(p)
    return acc
  }, {})

  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-50">
      <Nav />
      <div className="mx-auto max-w-5xl space-y-8 px-6 py-8">
        <h1 className="text-lg font-medium">预设库</h1>

        {Object.entries(grouped).map(([category, items]) => (
          <section key={category}>
            <p className="mb-3 text-xs uppercase tracking-wide text-zinc-500">
              {CATEGORY_LABELS[category] ?? category}
            </p>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {items.map((p) => (
                <div
                  key={p.biz_id}
                  className="rounded-xl border border-zinc-800 bg-zinc-900 p-4 transition hover:border-zinc-700"
                >
                  <p className="font-medium">{p.name}</p>
                  <p className="mt-1 text-xs leading-relaxed text-zinc-500">{p.prompt_fragment}</p>
                </div>
              ))}
            </div>
          </section>
        ))}
      </div>
    </div>
  )
}
