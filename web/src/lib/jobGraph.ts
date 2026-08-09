import type { JobNode, JobResponse } from './api'

export interface GraphNode {
  id: string
  label: string
  phase: string
  error: string
  outputs: Record<string, unknown> | null
  sublabel?: string // e.g. "3/4 完成" for a Loop container
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
// job.nodes flattens Loop iterations without unique per-iteration names
// (every image.comic4 panel shows up as a same-named "gen-one-panel" node,
// confirmed against a real run) — there's no way to address them
// individually from this API shape, so a Loop container is rendered as one
// node annotated with a "k/n done" count instead of expanding iterations.
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
  }
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
      const summary = loopSummary(nodes, 'gen-one-panel')
      const panels = toGraphNode(
        { name: 'panels', phase: summary.phase, outputs: null, error: '' },
        'panels',
        '四格生成',
        `${summary.done}/${summary.total || 4} 完成`,
      )
      const compose = toGraphNode(find(nodes, 'compose'), 'compose', '拼接')
      return { nodes: [panels, compose], edges: [{ source: 'panels', target: 'compose' }] }
    }
    case 'image.sequence': {
      const summary = loopSummary(nodes, 'gen-one-shot')
      const shots = toGraphNode(find(nodes, 'shots'), 'shots', '连续生成', `${summary.done}/${summary.total} 完成`)
      return { nodes: [shots], edges: [] }
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
