import type { ReactNode } from 'react'
import Rail from './Rail'

// Every routed page mounts through this instead of repeating the rail +
// scroll-container boilerplate — see Rail.tsx's doc for why the nav moved
// off the top bar.
export default function AppShell({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-screen overflow-hidden bg-zinc-950 text-zinc-50">
      <Rail />
      <div className="min-w-0 flex-1 overflow-y-auto">{children}</div>
    </div>
  )
}
