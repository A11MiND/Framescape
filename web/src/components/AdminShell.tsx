import type { ReactNode } from 'react'

// The admin console layout route (app/shell/AdminLayout) provides the
// navigation; this keeps the content width of pages not yet rebuilt.
export default function AdminShell({ children }: { children: ReactNode }) {
  return <div className="mx-auto max-w-6xl px-6 py-8">{children}</div>
}
