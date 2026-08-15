import type { JobNode, JobResponse } from './api'

export interface GraphNode {
  id: string
  label: string
  phase: string
  error: string
  outputs: Record<string, unknown> | null
  sublabel?: string // e.g. "3/4 完成" for a Loop container
  creditCost?: number
  startedAt?: string | null
  finishedAt?: string | null
  // name/loopIndex are the raw job_nodes identity (job.nodes[].name /
  // .loop_index), separate from `id` (which for loop iterations is a
  // synthetic "name[index]" string) — WorkflowGraph's retry button needs
  // these two values apart to call POST /jobs/{id}/nodes/{name}/retry.
  // undefined for the aggregate-placeholder/non-loop-iteration nodes that
  // never came from a real JobNode row (see toGraphNode's `n?` param).
  name?: string
  loopIndex?: number
}

export interface JobGraph {
  nodes: GraphNode[]
  edges: { source: string; target: string }[]
}

// Every workflow this platform runs is, in practice, a linear chain — even
// video.sequence's redo/upgrade loops were deliberately made sequential
// rather than parallel (docs/aether-validation-report.md §八's reentrancy
// workaround), so a simple left-to-right layout covers every real topology
// without needing a real graph-layout algorithm.
//
// loopSummary is the fallback for the brief window before a Loop's
// iterations exist yet as rows at all (submitted but not dispatched) —
// once they do, loopIterationNodes below renders each one individually
// instead. Earlier versions of this file believed Loop iterations could
// never be addressed individually at all ("every image.comic4 panel shows
// up as a same-named node, no way to tell them apart") — that turned out
// to be an API gap, not an engine one: the engine already resolves each
// iteration's own loop_index correctly (workflow.NodeState.LoopIndex, real
// scope-tree data), GET /jobs/{bizID} just wasn't returning it. Fixed
// server-side; this file's per-iteration rendering is what that unlocks.
function loopSummary(nodes: JobNode[], bodyName: string): { done: number; total: number; phase: string } {
  const body = nodes.filter((n) => n.name === bodyName)
  const done = body.filter((n) => n.phase === 'Succeeded').length
  const failed = body.some((n) => n.phase === 'Failed' || n.phase === 'Error')
  const running = body.some((n) => n.phase === 'Running')
  const phase = failed ? 'Failed' : done === body.length && body.length > 0 ? 'Succeeded' : running ? 'Running' : 'Pending'
  return { done, total: body.length, phase }
}

function find(nodes: JobNode[], name: string): JobNode | undefined {
  return nodes.find((n) => n.name === name)
}

function toGraphNode(n: JobNode | undefined, id: string, label: string, sublabel?: string): GraphNode {
  return {
    id,
    label,
    phase: n?.phase ?? 'Pending',
    error: n?.error ?? '',
    outputs: n?.outputs ?? null,
    sublabel,
    creditCost: n?.credit_cost,
    startedAt: n?.started_at,
    finishedAt: n?.finished_at,
    name: n?.name,
    loopIndex: n?.loop_index,
  }
}

// One GraphNode per real loop_index — §19.4.3's "4 个格子并行生成，每格独立
// 显示自己的状态" applied directly, instead of one aggregate "k/n done" box.
function loopIterationNodes(nodes: JobNode[], bodyName: string, labelFor: (i: number) => string): GraphNode[] {
  return nodes
    .filter((n) => n.name === bodyName && n.loop_index >= 0)
    .sort((a, b) => a.loop_index - b.loop_index)
    .map((n) => toGraphNode(n, `${bodyName}[${n.loop_index}]`, labelFor(n.loop_index)))
}

export function buildJobGraph(job: JobResponse): JobGraph {
  const nodes = job.nodes.filter((n) => n.name !== 'main')

  switch (job.workflow_name) {
    case 'image.single':
    case 'image.batch': {
      const gen = toGraphNode(find(nodes, 'gen'), 'gen', '生成')
      return { nodes: [gen], edges: [] }
    }
    case 'image.comic4': {
      const compose = toGraphNode(find(nodes, 'compose'), 'compose', '拼接')
      const panelNodes = loopIterationNodes(nodes, 'gen-one-panel', (i) => `格 ${i + 1}`)
      if (panelNodes.length === 0) {
        // Submitted but the Loop hasn't created its iteration rows yet —
        // show the aggregate placeholder rather than an empty graph.
        const summary = loopSummary(nodes, 'gen-one-panel')
        const panels = toGraphNode(
          { name: 'panels', phase: summary.phase, outputs: null, error: '', loop_index: -1 },
          'panels',
          '四格生成',
          `${summary.done}/${summary.total || 4} 完成`,
        )
        return { nodes: [panels, compose], edges: [{ source: 'panels', target: 'compose' }] }
      }
      return {
        nodes: [...panelNodes, compose],
        edges: panelNodes.map((p) => ({ source: p.id, target: 'compose' })),
      }
    }
    case 'image.sequence': {
      const shotNodes = loopIterationNodes(nodes, 'gen-one-shot', (i) => `第 ${i + 1} 张`)
      if (shotNodes.length === 0) {
        const summary = loopSummary(nodes, 'gen-one-shot')
        const shots = toGraphNode(
          { name: 'shots', phase: summary.phase, outputs: null, error: '', loop_index: -1 },
          'shots',
          '连续生成',
          `${summary.done}/${summary.total} 完成`,
        )
        return { nodes: [shots], edges: [] }
      }
      // No edges between them — unlike video.sequence's shots (which
      // genuinely chain via i2va/tail-frame continuity), image.sequence's
      // shots are independent Loop iterations with no dependency on each
      // other, so parallel sibling boxes is the accurate topology, not a
      // simplification.
      return { nodes: shotNodes, edges: [] }
    }
    case 'video.single': {
      const gen = toGraphNode(find(nodes, 'gen'), 'gen', '视频生成')
      const extract = toGraphNode(find(nodes, 'extract'), 'extract', '抽取首尾帧')
      return { nodes: [gen, extract], edges: [{ source: 'gen', target: 'extract' }] }
    }
    case 'video.sequence': {
      const shotIndexes = nodes
        .filter((n) => /^shot-\d+$/.test(n.name))
        .map((n) => Number(n.name.slice(5)))
        .sort((a, b) => a - b)

      const out: GraphNode[] = []
      const edges: { source: string; target: string }[] = []
      let prev: string | null = null
      for (const i of shotIndexes) {
        const shotId = `shot-${i}`
        out.push(toGraphNode(find(nodes, shotId), shotId, `第 ${i} 段`))
        if (prev) edges.push({ source: prev, target: shotId })
        prev = shotId
        const extractId = `shot-${i}-extract`
        const extractNode = find(nodes, extractId)
        if (extractNode) {
          out.push(toGraphNode(extractNode, extractId, `第 ${i} 段抽帧`))
          edges.push({ source: shotId, target: extractId })
          prev = extractId
        }
      }
      const gate = find(nodes, 'gate')
      if (gate || shotIndexes.length > 0) {
        out.push(toGraphNode(gate, 'gate', '预览门'))
        if (prev) edges.push({ source: prev, target: 'gate' })
        prev = 'gate'
      }
      const redo = find(nodes, 'redo')
      if (redo) {
        out.push(toGraphNode(redo, 'redo', '重做'))
        edges.push({ source: 'gate', target: 'redo' })
        prev = 'redo'
      }
      const upgrade = find(nodes, 'upgrade')
      if (upgrade) {
        out.push(toGraphNode(upgrade, 'upgrade', '升级 2K'))
        edges.push({ source: prev ?? 'gate', target: 'upgrade' })
        prev = 'upgrade'
      }
      const concat = find(nodes, 'concat')
      if (concat) {
        out.push(toGraphNode(concat, 'concat', '合成'))
        edges.push({ source: prev ?? 'gate', target: 'concat' })
      }
      return { nodes: out, edges }
    }
    default:
      return { nodes: nodes.map((n) => toGraphNode(n, n.name, n.name)), edges: [] }
  }
}
