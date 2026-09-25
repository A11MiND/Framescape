import type { ReactNode } from 'react'

// The shell is provided by the app layout route (app/shell/AppLayout);
// pages not yet rebuilt still wrap themselves in this.
export default function AppShell({ children }: { children: ReactNode }) {
  return <>{children}</>
}
