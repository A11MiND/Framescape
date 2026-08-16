import { useMemo, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ReactFlow, Background, Controls, Handle, Position, type NodeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { api, ApiError, type JobResponse } from '../lib/api'
import { buildJobGraph, type GraphNode } from '../lib/jobGraph'
import PhaseBadge, { phaseStyle } from './PhaseBadge'
import { displayNodeError } from '../lib/errors'
import { useToast } from './Toast'

// Mirrors jobsvc.retryableNodes exactly — both are single leaf `task`
// templates with string-only inputs, so a satellite retry (jobsvc.
// RetryNode's own doc) can safely rebuild their inputs. Kept as a small
// frontend allowlist rather than trusting the backend's error message
// alone, so the retry button simply doesn't render for anything else
// instead of rendering-then-failing.
const RETRYABLE_NODE: Record<string, string> = {
  'image.comic4': 'gen-one-panel',
  'image.sequence': 'gen-one-shot',
}
const FAILED_PHASES = new Set(['Failed', 'Error', 'Timeout'])

const X_SPACING = 200

function TaskNode({ data }: NodeProps) {
  const d = data as unknown as GraphNode
  const s = phaseStyle(d.phase)
  return (
    <div className={`rounded-xl border-2 bg-zinc-900 px-4 py-2.5 text-center ${s.className.split(' ').filter((c) => c.startsWith('border-')).join(' ')}`}>
      <Handle type="target" position={Position.Left} className="!bg-zinc-600" />
      <p className="text-sm font-medium text-zinc-100">{d.label}</p>
      {d.sublabel && <p className="text-xs text-zinc-500">{d.sublabel}</p>}
      <div className="mt-1">
        <PhaseBadge phase={d.phase} />
      </div>
      <Handle type="source" position={Position.Right} className="!bg-zinc-600" />
    </div>
  )
}

const nodeTypes = { task: TaskNode }

function formatDuration(startedAt: string | null | undefined, finishedAt: string | null | undefined, inProgressLabel: string): string {
  if (!startedAt) return ''
  if (!finishedAt) return inProgressLabel
  const ms = new Date(finishedAt).getTime() - new Date(startedAt).getTime()
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`
}

// F7.2: react-flow rendering of the job's DAG, phase-colored per §19.2,
// click-for-detail per §19.4.6 (input/output/耗时/消耗积分/错误信息 — the
// node detail drawer this whole component's side panel is). Loop
// iterations render as individual nodes per jobGraph.ts's own doc; the
// side panel's timing/cost come from job_nodes (the projection table,
// populated by internal/application/projection — see handleGetJob's doc
// for why this data lives there and not on the engine's own NodeState).
export default function WorkflowGraph({ job }: { job: JobResponse }) {
  const { t } = useTranslation()
  const graph = useMemo(() => buildJobGraph(job, t), [job, t])
  const [selected, setSelected] = useState<GraphNode | null>(null)
  const [override, setOverride] = useState('')
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const pushToast = useToast()

  const retryNode = useMutation({
    mutationFn: (node: GraphNode) =>
      api.retryNode(job.biz_id, node.name!, node.loopIndex!, override.trim() || undefined),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['jobs'] })
      navigate(`/jobs/${data.biz_id}`)
    },
    onError: (err) =>
      pushToast(err instanceof ApiError ? err.message : t('workflowGraph.retryFailed'), () => selected && retryNode.mutate(selected)),
  })

  const flowNodes = graph.nodes.map((n, i) => ({
    id: n.id,
    type: 'task',
    position: { x: i * X_SPACING, y: 0 },
    data: n as unknown as Record<string, unknown>,
  }))
  const flowEdges = graph.edges.map((e) => ({
    id: `${e.source}-${e.target}`,
    source: e.source,
    target: e.target,
    animated: graph.nodes.find((n) => n.id === e.source)?.phase === 'Running',
  }))

  return (
    <div className="relative">
      <div style={{ height: 220 }} className="rounded-xl border border-zinc-800 bg-zinc-950">
        <ReactFlow
          nodes={flowNodes}
          edges={flowEdges}
          nodeTypes={nodeTypes}
          onNodeClick={(_, node) => setSelected(node.data as unknown as GraphNode)}
          fitView
          proOptions={{ hideAttribution: true }}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable
        >
          <Background color="#3f3f46" gap={16} />
          {/* react-flow's Controls buttons keep their own light background
              (--xy-controls-button-background-color-default), but the icon
              uses currentColor — without an explicit color here it inherits
              this app's near-white body text color, rendering an invisible
              white-on-white icon. Force a dark color to match the widget's
              own light theme regardless of our app-wide dark theme. */}
          <Controls showInteractive={false} style={{ color: '#18181b' }} />
        </ReactFlow>
      </div>

      {selected && (
        <div className="absolute inset-y-0 right-0 w-72 overflow-y-auto rounded-r-xl border-l border-zinc-800 bg-zinc-900 p-4 shadow-2xl">
          <div className="mb-3 flex items-center justify-between">
            <p className="font-medium text-zinc-100">{selected.label}</p>
            <button onClick={() => setSelected(null)} className="text-zinc-500 hover:text-zinc-300">
              ×
            </button>
          </div>
          <PhaseBadge phase={selected.phase} />
          {(selected.startedAt || !!selected.creditCost) && (
            <div className="mt-3 flex gap-4 text-xs text-zinc-500">
              {selected.startedAt && (
                <span>{t('workflowGraph.duration', { duration: formatDuration(selected.startedAt, selected.finishedAt, t('workflowGraph.inProgress')) })}</span>
              )}
              {!!selected.creditCost && (
                <span>
                  {t('workflowGraph.cost')} <span className="text-violet-400">✦ {selected.creditCost}</span>
                </span>
              )}
            </div>
          )}
          {selected.error && (
            <p className="mt-3 text-sm text-red-400">{displayNodeError(selected.error, t)}</p>
          )}
          {selected.name &&
            selected.loopIndex !== undefined &&
            selected.loopIndex >= 0 &&
            FAILED_PHASES.has(selected.phase) &&
            RETRYABLE_NODE[job.workflow_name] === selected.name && (
              <div className="mt-3 space-y-2 border-t border-zinc-800 pt-3">
                <input
                  value={override}
                  onChange={(e) => setOverride(e.target.value)}
                  placeholder={t('workflowGraph.retryPromptPlaceholder')}
                  className="w-full rounded-md border border-zinc-700 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-200 placeholder:text-zinc-600"
                />
                <button
                  onClick={() => retryNode.mutate(selected)}
                  disabled={retryNode.isPending}
                  className="w-full rounded-md bg-violet-600 py-1.5 text-xs font-medium text-white hover:bg-violet-500 disabled:opacity-50"
                >
                  {retryNode.isPending ? t('workflowGraph.retrying') : t('workflowGraph.retryThis')}
                </button>
              </div>
            )}
          {selected.outputs && (
            <pre className="mt-3 overflow-x-auto rounded-lg bg-zinc-950 p-2 text-xs text-zinc-400">
              {JSON.stringify(selected.outputs, null, 2)}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}
