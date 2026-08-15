import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type Project } from '../lib/api'
import AppShell from '../components/AppShell'
import { useToast } from '../components/Toast'

// §the composer-blueprint artifact's "/projects CRUD" gap: a place to file
// assets under (Assets.tsx's own project filter dropdown is the other half
// of this — see handleListAssets's ?project_id= doc). jobs/characters stay
// unassociated for now, same scoping note as migrations/00007's own doc.
export default function Projects() {
  const [creating, setCreating] = useState(false)
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.listProjects })

  return (
    <AppShell>
      <div className="mx-auto max-w-3xl px-6 py-8">
        <div className="mb-6 flex items-center justify-between">
          <h1 className="text-lg font-medium">项目</h1>
          <button
            onClick={() => setCreating((v) => !v)}
            className="rounded-lg bg-violet-500 px-4 py-2 text-sm font-medium text-white hover:bg-violet-400"
          >
            {creating ? '取消' : '+ 新建项目'}
          </button>
        </div>

        {creating && <ProjectForm onDone={() => setCreating(false)} />}

        {projects.isSuccess && projects.data.projects.length === 0 && !creating && (
          <p className="text-zinc-500">还没有项目。创建一个，再去资产库把素材归类进去。</p>
        )}

        <div className="space-y-3">
          {projects.data?.projects.map((p) => <ProjectRow key={p.biz_id} project={p} />)}
        </div>
      </div>
    </AppShell>
  )
}

function ProjectRow({ project }: { project: Project }) {
  const [editing, setEditing] = useState(false)
  const qc = useQueryClient()
  const pushToast = useToast()

  const del = useMutation({
    mutationFn: () => api.deleteProject(project.biz_id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['projects'] }),
    onError: () => pushToast('删除失败，请重试', () => del.mutate()),
  })

  if (editing) {
    return <ProjectForm project={project} onDone={() => setEditing(false)} onCancel={() => setEditing(false)} />
  }

  return (
    <div className="flex items-start justify-between gap-3 rounded-xl border border-zinc-800 bg-zinc-900 p-4">
      <div className="min-w-0">
        <Link to={`/assets?project_id=${project.biz_id}`} className="font-medium hover:text-violet-400">
          {project.name}
        </Link>
        {project.description && (
          <p className="mt-1 line-clamp-2 text-sm text-zinc-400">{project.description}</p>
        )}
      </div>
      <div className="flex shrink-0 gap-1">
        <button
          onClick={() => setEditing(true)}
          title="编辑"
          className="rounded-lg px-2 py-1 text-xs text-zinc-500 transition hover:bg-zinc-800 hover:text-zinc-200"
        >
          ✎
        </button>
        <button
          onClick={() => del.mutate()}
          disabled={del.isPending}
          title="删除"
          className="rounded-lg px-2 py-1 text-xs text-zinc-500 transition hover:bg-zinc-800 hover:text-red-400 disabled:opacity-50"
        >
          ✕
        </button>
      </div>
    </div>
  )
}

function ProjectForm({
  project,
  onDone,
  onCancel,
}: {
  project?: Project
  onDone: () => void
  onCancel?: () => void
}) {
  const [name, setName] = useState(project?.name ?? '')
  const [description, setDescription] = useState(project?.description ?? '')
  const qc = useQueryClient()

  const save = useMutation({
    mutationFn: () =>
      project
        ? api.updateProject(project.biz_id, { name, description })
        : api.createProject(name, description),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['projects'] })
      onDone()
    },
  })

  return (
    <div className="mb-6 space-y-3 rounded-xl border border-zinc-800 bg-zinc-900 p-5">
      <input
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="项目名"
        className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
      />
      <textarea
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        rows={2}
        placeholder="说明（可选）"
        className="w-full resize-none rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-violet-500"
      />
      {save.isError && <p className="text-sm text-red-400">{(save.error as Error).message}</p>}
      <div className="flex gap-2">
        <button
          onClick={() => save.mutate()}
          disabled={!name.trim() || save.isPending}
          className="rounded-lg bg-violet-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
        >
          {save.isPending ? '保存中…' : project ? '保存' : '创建项目'}
        </button>
        {onCancel && (
          <button
            onClick={onCancel}
            className="rounded-lg px-4 py-2 text-sm text-zinc-400 transition hover:bg-zinc-800"
          >
            取消
          </button>
        )}
      </div>
    </div>
  )
}
