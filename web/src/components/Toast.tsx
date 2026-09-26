import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { X } from 'lucide-react'

// Short confirmations and recoverable failures. A toast with a retry stays
// until acted on or dismissed; one with another action (such as undo) stays
// long enough to use it; plain ones leave after 6 seconds.
export interface ToastAction {
  label: string
  onClick: () => void
}

interface Toast {
  id: number
  message: string
  action?: ToastAction
  sticky: boolean
}

type Push = (message: string, action?: (() => void) | ToastAction) => void

const ToastContext = createContext<Push | null>(null)

export function useToast() {
  const push = useContext(ToastContext)
  if (!push) throw new Error('useToast must be used within ToastProvider')
  return push
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation('ui')
  const [toasts, setToasts] = useState<Toast[]>([])
  const nextId = useRef(0)

  const push = useCallback<Push>(
    (message, action) => {
      const id = nextId.current++
      const retry = typeof action === 'function'
      const toast: Toast = { id, message, sticky: retry, action: retry ? { label: t('action.retry'), onClick: action } : action }
      setToasts((cur) => [...cur, toast])
      if (!retry) setTimeout(() => setToasts((cur) => cur.filter((x) => x.id !== id)), action ? 10000 : 6000)
    },
    [t],
  )

  const dismiss = (id: number) => setToasts((cur) => cur.filter((x) => x.id !== id))

  return (
    <ToastContext.Provider value={push}>
      {children}
      <div aria-live="polite" className="fixed right-4 bottom-[calc(72px+env(safe-area-inset-bottom))] z-50 flex max-w-[calc(100vw-2rem)] flex-col gap-2 lg:bottom-4">
        {toasts.map((toast) => (
          <div key={toast.id} role="status" className="flex items-center gap-3 rounded-card border border-border bg-surface px-4 py-3 text-body text-fg shadow-overlay">
            <span className="min-w-0 flex-1">{toast.message}</span>
            {toast.action && (
              <button
                type="button"
                onClick={() => {
                  toast.action?.onClick()
                  dismiss(toast.id)
                }}
                className="shrink-0 rounded-[8px] px-2.5 py-1 text-body font-medium text-primary-text hover:bg-primary-soft"
              >
                {toast.action.label}
              </button>
            )}
            <button type="button" aria-label={t('action.close')} onClick={() => dismiss(toast.id)} className="shrink-0 rounded-[8px] p-1 text-fg-muted hover:bg-surface-2 hover:text-fg">
              <X aria-hidden className="size-4" />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}
