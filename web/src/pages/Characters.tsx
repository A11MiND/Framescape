import { useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Character } from '../lib/api'
import AppShell from '../components/AppShell'
import { AssetPicker } from '../components/AssetPicker'

// F3.1: name + description + 1-3 reference images + a fixed seed. There's no
// raw upload endpoint (PRD §11 — assets only exist as generation
// side-effects), so the ref-image picker draws from the user's own already-
// generated image assets rather than a file input.
export default function Characters() {
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
          <h1 className="text-lg font-medium">角色库</h1>
          <button
            onClick={() => setCreating((v) => !v)}
            className="rounded-lg bg-violet-500 px-4 py-2 text-sm font-medium text-white hover:bg-violet-400"
          >
            {creating ? '取消' : '+ 新建角色'}
          </button>
        </div>

        {creating && (
          <CreateCharacterForm
            initialSelected={prefillAssetId ? [prefillAssetId] : []}
            onDone={() => {
              setCreating(false)
              characters.refetch()
            }}
          />
        )}

        {characters.isSuccess && characters.data.characters.length === 0 && !creating && (
          <p className="text-zinc-500">还没有角色。先在创作台生成几张图，再回来把它们定为角色参考图。</p>
        )}

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {characters.data?.characters.map((c) => <CharacterCard key={c.biz_id} character={c} />)}
        </div>
      </div>
    </AppShell>
  )
}

function CharacterCard({ character }: { character: Character }) {
  return (
    <div className="rounded-xl border border-zinc-800 bg-zinc-900 p-4">
      <div className="mb-3 flex gap-2">
        {character.ref_asset_ids.map((id) => (
          <RefThumb key={id} assetId={id} />
        ))}
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

function CreateCharacterForm({
  initialSelected,
  onDone,
}: {
  initialSelected: string[]
  onDone: () => void
}) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [seed, setSeed] = useState(() => Math.floor(Math.random() * 1_000_000))
  const [selected, setSelected] = useState<string[]>(initialSelected)
  const qc = useQueryClient()

  const create = useMutation({
    mutationFn: () => api.createCharacter(name, description, selected, seed),
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
      <div className="grid grid-cols-2 gap-3">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="角色名"
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
        placeholder="外观描述（可选）"
        className="w-full resize-none rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
      />

      <div>
        <p className="mb-2 text-xs uppercase tracking-wide text-zinc-500">
          选择 1-3 张参考图（已选 {selected.length}）
        </p>
        <AssetPicker
          type="image"
          selected={selected}
          onToggle={toggle}
          max={3}
          emptyHint="还没有生成过图片，先去创作台生成一些"
        />
      </div>

      {create.isError && <p className="text-sm text-red-400">{(create.error as Error).message}</p>}

      <button
        onClick={() => create.mutate()}
        disabled={!canSubmit || create.isPending}
        className="rounded-lg bg-violet-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
      >
        {create.isPending ? '创建中…' : '创建角色'}
      </button>
    </div>
  )
}
