import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, presetDisplayName, type Preset } from '../lib/api'
import AppShell from '../components/AppShell'
import { useToast } from '../components/Toast'

const CATEGORIES = ['style', 'pose', 'composition', 'lighting', 'camera']

// F4.3/F4.5: was a pure browse/reference view — the only way to add a
// preset was "另存為我的預設" from inside Studio's composer, which only
// ever captures whatever the active tab's own text field currently holds
// as prompt_fragment. §07 gap: a user asked why this page itself had no
// create entry point, since it's the natural place to browse *and* manage
// the library, not just look at it. Reuses the same POST /presets a saved-
// from-Studio preset already goes through — "mine" presets look identical
// regardless of which surface created them.
export default function Presets() {
  const { t, i18n } = useTranslation()
  const qc = useQueryClient()
  const pushToast = useToast()
  const presets = useQuery({ queryKey: ['presets'], queryFn: () => api.listPresets() })
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [category, setCategory] = useState('style')
  const [styleType, setStyleType] = useState('')
  const [fragment, setFragment] = useState('')

  const create = useMutation({
    mutationFn: () =>
      api.createPreset({ name, category, prompt_fragment: fragment, style_type: styleType || undefined }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['presets'] })
      setCreating(false)
      setName('')
      setStyleType('')
      setFragment('')
    },
    onError: () => pushToast(t('presets.createFailed'), () => create.mutate()),
  })

  const del = useMutation({
    mutationFn: (bizId: string) => api.deletePreset(bizId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['presets'] }),
    onError: () => pushToast(t('presets.deleteFailed')),
  })

  const grouped = (presets.data?.presets ?? []).reduce<Record<string, Preset[]>>((acc, p) => {
    ;(acc[p.category] ??= []).push(p)
    return acc
  }, {})

  return (
    <AppShell>
      <div className="mx-auto max-w-5xl space-y-8 px-6 py-8">
        <div className="flex items-center justify-between">
          <h1 className="text-lg font-medium">{t('presets.title')}</h1>
          <button
            onClick={() => setCreating((v) => !v)}
            className="rounded-lg bg-violet-500 px-4 py-2 text-sm font-medium text-white hover:bg-violet-400"
          >
            {creating ? t('common.cancel') : t('presets.new')}
          </button>
        </div>

        {creating && (
          <div className="space-y-3 rounded-xl border border-zinc-800 bg-zinc-900 p-5">
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={t('presets.namePlaceholder')}
                className="rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
              />
              <select
                value={category}
                onChange={(e) => setCategory(e.target.value)}
                className="rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-300 outline-none focus:border-violet-500"
              >
                {CATEGORIES.map((c) => (
                  <option key={c} value={c}>
                    {t(`presets.category.${c}`)}
                  </option>
                ))}
              </select>
            </div>
            <textarea
              value={fragment}
              onChange={(e) => setFragment(e.target.value)}
              rows={2}
              placeholder={t('presets.fragmentPlaceholder')}
              className="w-full resize-none rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
            />
            {category === 'style' && (
              <input
                value={styleType}
                onChange={(e) => setStyleType(e.target.value)}
                placeholder={t('presets.styleTypePlaceholder')}
                className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
              />
            )}
            {create.isError && <p className="text-sm text-red-400">{(create.error as Error).message}</p>}
            <button
              onClick={() => create.mutate()}
              disabled={!name.trim() || !fragment.trim() || create.isPending}
              className="rounded-lg bg-violet-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
            >
              {create.isPending ? t('common.saving') : t('presets.create')}
            </button>
          </div>
        )}

        {Object.entries(grouped).map(([category, items]) => (
          <section key={category}>
            <p className="mb-3 text-xs uppercase tracking-wide text-zinc-500">
              {t(`presets.category.${category}`, category)}
            </p>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {items.map((p) => (
                <div
                  key={p.biz_id}
                  className="group relative overflow-hidden rounded-2xl border border-zinc-800 bg-zinc-900 transition hover:border-zinc-700"
                >
                  {p.cover_url ? (
                    <img src={p.cover_url} alt="" className="aspect-square w-full object-cover" />
                  ) : (
                    <div className="aspect-square w-full bg-zinc-800" />
                  )}
                  {p.mine && (
                    <button
                      type="button"
                      onClick={() => del.mutate(p.biz_id)}
                      disabled={del.isPending && del.variables === p.biz_id}
                      title={t('common.delete')}
                      className="absolute right-2 top-2 flex h-7 w-7 items-center justify-center rounded-full bg-black/70 text-sm text-zinc-300 opacity-60 transition hover:text-red-400 hover:opacity-100 focus-visible:opacity-100 group-hover:opacity-100 disabled:opacity-50"
                    >
                      ✕
                    </button>
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
