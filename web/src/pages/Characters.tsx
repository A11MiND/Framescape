import { useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, type Character } from '../lib/api'
import AppShell from '../components/AppShell'
import { AssetPicker } from '../components/AssetPicker'
import { useToast } from '../components/Toast'

// F3.1: name + description + 1-3 reference images + a fixed seed. Ref
// images come from AssetPicker, which now (F2.1, batch 2) lets the user
// upload their own straight from this form instead of only ever picking
// from assets a generation already produced.
export default function Characters() {
  const { t } = useTranslation()
  const location = useLocation()
  const prefillAssetId = (location.state as { prefillAssetId?: string } | null)?.prefillAssetId
  const [creating, setCreating] = useState(!!prefillAssetId)
  const characters = useQuery({ queryKey: ['characters'], queryFn: api.listCharacters })

  // Studio's "存为角色" / "抽帧存为角色" suggestions (§19.4.2) land here with
  // the just-generated asset preselected — opens the form instead of making
  // the user hunt for the same thumbnail again in the picker below.
  useEffect(() => {
    if (prefillAssetId) setCreating(true)
  }, [prefillAssetId])

  return (
    <AppShell>
      <div className="mx-auto max-w-5xl px-6 py-8">
        <div className="mb-6 flex items-center justify-between">
          <h1 className="text-lg font-medium">{t('characters.title')}</h1>
          <button
            onClick={() => setCreating((v) => !v)}
            className="rounded-lg bg-violet-500 px-4 py-2 text-sm font-medium text-white hover:bg-violet-400"
          >
            {creating ? t('common.cancel') : t('characters.new')}
          </button>
        </div>

        {creating && (
          <CharacterForm
            initialSelected={prefillAssetId ? [prefillAssetId] : []}
            onDone={() => {
              setCreating(false)
              characters.refetch()
            }}
          />
        )}

        {characters.isSuccess && characters.data.characters.length === 0 && !creating && (
          <p className="text-zinc-500">{t('characters.empty')}</p>
        )}

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {characters.data?.characters.map((c) => <CharacterCard key={c.biz_id} character={c} />)}
        </div>
      </div>
    </AppShell>
  )
}

function CharacterCard({ character }: { character: Character }) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState(false)
  const qc = useQueryClient()
  const pushToast = useToast()

  const del = useMutation({
    mutationFn: () => api.deleteCharacter(character.biz_id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['characters'] }),
    onError: () => pushToast(t('characters.deleteFailed'), () => del.mutate()),
  })

  if (editing) {
    return <CharacterForm character={character} onDone={() => setEditing(false)} onCancel={() => setEditing(false)} />
  }

  return (
    <div className="rounded-xl border border-zinc-800 bg-zinc-900 p-4">
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="flex flex-wrap gap-2">
          {character.ref_asset_ids.map((id) => (
            <RefThumb key={id} assetId={id} />
          ))}
        </div>
        <div className="flex shrink-0 gap-1">
          <button
            onClick={() => setEditing(true)}
            title={t('common.edit')}
            className="rounded-lg px-2 py-1 text-xs text-zinc-500 transition hover:bg-zinc-800 hover:text-zinc-200"
          >
            ✎
          </button>
          <button
            onClick={() => del.mutate()}
            disabled={del.isPending}
            title={t('common.delete')}
            className="rounded-lg px-2 py-1 text-xs text-zinc-500 transition hover:bg-zinc-800 hover:text-red-400 disabled:opacity-50"
          >
            ✕
          </button>
        </div>
      </div>
      <p className="font-medium">{character.name}</p>
      {character.description && (
        <p className="mt-1 line-clamp-2 text-sm text-zinc-400">{character.description}</p>
      )}
      <p className="mt-2 font-mono text-xs text-zinc-600">seed {character.seed}</p>
    </div>
  )
}

function RefThumb({ assetId }: { assetId: string }) {
  const { data } = useQuery({ queryKey: ['asset', assetId], queryFn: () => api.getAsset(assetId) })
  if (!data) return <div className="h-16 w-16 animate-pulse rounded-lg bg-zinc-800" />
  return <img src={data.public_url} alt="" className="h-16 w-16 rounded-lg object-cover" />
}

// Shared by both create (no `character` prop) and edit (CharacterCard's
// "✎" button) — the two flows differ only in which mutation they call and
// what the fields start out as, everything else (validation, layout,
// AssetPicker) is identical.
function CharacterForm({
  character,
  initialSelected,
  onDone,
  onCancel,
}: {
  character?: Character
  initialSelected?: string[]
  onDone: () => void
  onCancel?: () => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState(character?.name ?? '')
  const [description, setDescription] = useState(character?.description ?? '')
  const [seed, setSeed] = useState(() => character?.seed ?? Math.floor(Math.random() * 1_000_000))
  const [selected, setSelected] = useState<string[]>(character?.ref_asset_ids ?? initialSelected ?? [])
  const qc = useQueryClient()

  const save = useMutation({
    mutationFn: () =>
      character
        ? api.updateCharacter(character.biz_id, { name, description, ref_asset_ids: selected, seed })
        : api.createCharacter(name, description, selected, seed),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['characters'] })
      onDone()
    },
  })

  const toggle = (id: string) =>
    setSelected((cur) => {
      if (cur.includes(id)) return cur.filter((x) => x !== id)
      if (cur.length >= 3) return cur
      return [...cur, id]
    })

  const canSubmit = name.trim().length > 0 && selected.length >= 1 && selected.length <= 3

  return (
    <div className="mb-8 space-y-4 rounded-xl border border-zinc-800 bg-zinc-900 p-5">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={t('characters.namePlaceholder')}
          className="rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
        />
        <input
          type="number"
          value={seed}
          onChange={(e) => setSeed(Number(e.target.value))}
          placeholder="seed"
          className="rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
        />
      </div>
      <textarea
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        rows={2}
        placeholder={t('characters.descriptionPlaceholder')}
        className="w-full resize-none rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
      />

      <div>
        <p className="mb-2 text-xs uppercase tracking-wide text-zinc-500">
          {t('characters.pickRefs', { count: selected.length })}
        </p>
        <AssetPicker
          type="image"
          selected={selected}
          onToggle={toggle}
          max={3}
          emptyHint={t('characters.pickerEmptyHint')}
        />
      </div>

      {save.isError && <p className="text-sm text-red-400">{(save.error as Error).message}</p>}

      <div className="flex gap-2">
        <button
          onClick={() => save.mutate()}
          disabled={!canSubmit || save.isPending}
          className="rounded-lg bg-violet-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
        >
          {save.isPending ? t('common.saving') : character ? t('common.save') : t('characters.create')}
        </button>
        {onCancel && (
          <button
            onClick={onCancel}
            className="rounded-lg px-4 py-2 text-sm text-zinc-400 transition hover:bg-zinc-800"
          >
            {t('common.cancel')}
          </button>
        )}
      </div>
    </div>
  )
}
