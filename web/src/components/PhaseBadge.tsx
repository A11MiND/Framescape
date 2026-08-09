// PRD §19.2's single global phase→color→icon mapping — every place that
// shows a task/job phase (DAG nodes, job status, node detail drawer) draws
// from this table so "state is green but the icon is grey" can't happen.
const PHASE_STYLE: Record<string, { label: string; icon: string; className: string; pulse?: boolean }> = {
  Created: { label: '待创建', icon: '○', className: 'text-zinc-400 border-zinc-700 bg-zinc-800/50' },
  Ready: { label: '就绪', icon: '○', className: 'text-zinc-400 border-zinc-700 bg-zinc-800/50' },
  Pending: { label: '等待中', icon: '○', className: 'text-zinc-400 border-zinc-700 bg-zinc-800/50' },
  Running: { label: '生成中', icon: '⟳', className: 'text-sky-400 border-sky-600 bg-sky-500/10', pulse: true },
  Suspended: {
    label: '暂停中',
    icon: '⏸',
    className: 'text-amber-400 border-amber-600 bg-amber-500/10',
    pulse: true,
  },
  Succeeded: { label: '已完成', icon: '✓', className: 'text-emerald-400 border-emerald-600 bg-emerald-500/10' },
  Failed: { label: '失败', icon: '✕', className: 'text-red-400 border-red-600 bg-red-500/10' },
  Error: { label: '错误', icon: '✕', className: 'text-red-400 border-red-600 bg-red-500/10' },
  Timeout: { label: '超时', icon: '⏱✕', className: 'text-red-400 border-red-600 bg-red-500/10' },
  Skipped: { label: '已跳过', icon: '╱', className: 'text-zinc-400 border-zinc-700 bg-zinc-800/50' },
  Cancelled: { label: '已取消', icon: '⊘', className: 'text-zinc-400 border-zinc-700 bg-zinc-800/50' },
}

export function phaseStyle(phase: string) {
  return PHASE_STYLE[phase] ?? { label: phase || '未知', icon: '○', className: 'text-zinc-500 border-zinc-800' }
}

export default function PhaseBadge({ phase }: { phase: string }) {
  const s = phaseStyle(phase)
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs ${s.className} ${s.pulse ? 'animate-pulse' : ''}`}
    >
      <span>{s.icon}</span>
      {s.label}
    </span>
  )
}
