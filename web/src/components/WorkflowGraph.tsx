import { useMemo, useState } from 'react'
import { ReactFlow, Background, Controls, Handle, Position, type NodeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { JobResponse } from '../lib/api'
import { buildJobGraph, type GraphNode } from '../lib/jobGraph'
import PhaseBadge, { phaseStyle } from './PhaseBadge'
import { displayNodeError } from '../lib/errors'

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

function formatDuration(startedAt?: string | null, finishedAt?: string | null): string {
  if (!startedAt) return ''
  if (!finishedAt) return '进行中…'
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
  const graph = useMemo(() => buildJobGraph(job), [job])
  const [selected, setSelected] = useState<GraphNode | null>(null)

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
              {selected.startedAt && <span>耗时 {formatDuration(selected.startedAt, selected.finishedAt)}</span>}
              {!!selected.creditCost && (
                <span>
                  消耗 <span className="text-violet-400">✦ {selected.creditCost}</span>
                </span>
              )}
            </div>
          )}
          {selected.error && (
            <p className="mt-3 text-sm text-red-400">{displayNodeError(selected.error)}</p>
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
