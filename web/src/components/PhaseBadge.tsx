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
  // Was sky (blue) — the only place in the whole app using that color, and
  // the reason it needed its own light-theme correction that nothing
  // remembered to add (found live: "颜色不太对齐"). violet is already the
  // brand accent and already means "this one, active/current" everywhere
  // else (selected tab, selected preset, ...), so "running" reusing it is
  // more consistent, not less — and it's already correctly theme-corrected.
  Running: { labelKey: 'phase.running', icon: '⟳', className: 'text-violet-400 border-violet-600 bg-violet-500/10', pulse: true },
  Suspended: {
    labelKey: 'phase.suspended',
    // Was the pause-symbol codepoint (U+23F8) — its default presentation
    // is emoji (a colorful pause icon on most platforms), unlike every
    // other icon in this table.
    icon: '‖',
    className: 'text-amber-400 border-amber-600 bg-amber-500/10',
    pulse: true,
  },
  Succeeded: { labelKey: 'phase.succeeded', icon: '✓', className: 'text-emerald-400 border-emerald-600 bg-emerald-500/10' },
  Failed: { labelKey: 'phase.failed', icon: '✕', className: 'text-red-400 border-red-600 bg-red-500/10' },
  Error: { labelKey: 'phase.error', icon: '✕', className: 'text-red-400 border-red-600 bg-red-500/10' },
  // Was the stopwatch codepoint (U+23F1) plus a cross — the stopwatch
  // glyph is also default-emoji-presentation.
  Timeout: { labelKey: 'phase.timeout', icon: '⊗', className: 'text-red-400 border-red-600 bg-red-500/10' },
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
      className={`inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-full border px-2 py-0.5 text-xs ${s.className} ${s.pulse ? 'animate-pulse' : ''}`}
    >
      <span>{s.icon}</span>
      {t(s.labelKey)}
    </span>
  )
}
