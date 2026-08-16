import { useTranslation } from 'react-i18next'

// PRD §19.2's single global phase→color→icon mapping — every place that
// shows a task/job phase (DAG nodes, job status, node detail drawer) draws
// from this table so "state is green but the icon is grey" can't happen.
// labelKey (not a resolved string) since phaseStyle() is called from
// non-component contexts too (WorkflowGraph's TaskNode reads .className
// only) — only PhaseBadge itself resolves labelKey via t().
const PHASE_STYLE: Record<string, { labelKey: string; icon: string; className: string; pulse?: boolean }> = {
  Created: { labelKey: 'phase.created', icon: '○', className: 'text-zinc-400 border-zinc-700 bg-zinc-800/50' },
  Ready: { labelKey: 'phase.ready', icon: '○', className: 'text-zinc-400 border-zinc-700 bg-zinc-800/50' },
  Pending: { labelKey: 'phase.pending', icon: '○', className: 'text-zinc-400 border-zinc-700 bg-zinc-800/50' },
  Running: { labelKey: 'phase.running', icon: '⟳', className: 'text-sky-400 border-sky-600 bg-sky-500/10', pulse: true },
  Suspended: {
    labelKey: 'phase.suspended',
    icon: '⏸',
    className: 'text-amber-400 border-amber-600 bg-amber-500/10',
    pulse: true,
  },
  Succeeded: { labelKey: 'phase.succeeded', icon: '✓', className: 'text-emerald-400 border-emerald-600 bg-emerald-500/10' },
  Failed: { labelKey: 'phase.failed', icon: '✕', className: 'text-red-400 border-red-600 bg-red-500/10' },
  Error: { labelKey: 'phase.error', icon: '✕', className: 'text-red-400 border-red-600 bg-red-500/10' },
  Timeout: { labelKey: 'phase.timeout', icon: '⏱✕', className: 'text-red-400 border-red-600 bg-red-500/10' },
  Skipped: { labelKey: 'phase.skipped', icon: '╱', className: 'text-zinc-400 border-zinc-700 bg-zinc-800/50' },
  Cancelled: { labelKey: 'phase.cancelled', icon: '⊘', className: 'text-zinc-400 border-zinc-700 bg-zinc-800/50' },
}

export function phaseStyle(phase: string) {
  return PHASE_STYLE[phase] ?? { labelKey: 'phase.unknown', icon: '○', className: 'text-zinc-500 border-zinc-800' }
}

export default function PhaseBadge({ phase }: { phase: string }) {
  const { t } = useTranslation()
  const s = phaseStyle(phase)
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs ${s.className} ${s.pulse ? 'animate-pulse' : ''}`}
    >
      <span>{s.icon}</span>
      {t(s.labelKey)}
    </span>
  )
}
