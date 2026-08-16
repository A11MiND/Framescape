import type { ReactNode } from 'react'
import Rail from './Rail'

// Every routed page mounts through this instead of repeating the rail +
// scroll-container boilerplate — see Rail.tsx's doc for why the nav moved
// off the top bar. Below `lg` (1024px, §4th-batch's own threshold) the rail
// itself switches from a left sidebar to a horizontal top bar (Rail.tsx's
// own responsive classes), so the outer flex direction has to flip to
// match — stacked top-to-bottom on narrow screens, side-by-side on wide
// ones. Not visually verified in this environment (viewport-resize
// automation reports success but never actually changes window.innerWidth
// here, see DEV_PLAN.md) — built from well-understood Tailwind responsive
// primitives specifically because that makes it safe to ship without the
// visual check; still worth confirming on a real narrow device.
export default function AppShell({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-screen flex-col overflow-hidden bg-zinc-950 text-zinc-50 lg:flex-row">
      <Rail />
      <div className="min-w-0 flex-1 overflow-y-auto">{children}</div>
    </div>
  )
}
