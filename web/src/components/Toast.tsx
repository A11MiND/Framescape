import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

// §19.5.3's Toast scope is deliberately narrow — only "提交后网络错误"
// (retryable). Insufficient-balance and content-moderation blocks are
// inline/blocking by the same table, and partial single-node failures don't
// interrupt at all: none of those call useToast(). Toasts auto-dismiss after
// 6s unless they carry a retry action, since a retryable toast that vanishes
// before the user can click it defeats the point.
interface Toast {
  id: number
  message: string
  onRetry?: () => void
}

const ToastContext = createContext<((message: string, onRetry?: () => void) => void) | null>(null)

export function useToast() {
  const push = useContext(ToastContext)
  if (!push) throw new Error('useToast must be used within ToastProvider')
  return push
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const [toasts, setToasts] = useState<Toast[]>([])
  const nextId = useRef(0)

  const push = useCallback((message: string, onRetry?: () => void) => {
    const id = nextId.current++
    setToasts((cur) => [...cur, { id, message, onRetry }])
    if (!onRetry) {
      setTimeout(() => setToasts((cur) => cur.filter((t) => t.id !== id)), 6000)
    }
  }, [])

  const dismiss = (id: number) => setToasts((cur) => cur.filter((t) => t.id !== id))

  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2">
        {toasts.map((toast) => (
          <div
            key={toast.id}
            className="flex items-center gap-3 rounded-lg border border-zinc-700 bg-zinc-900 px-4 py-3 text-sm text-zinc-200 shadow-lg"
          >
            <span>{toast.message}</span>
            {toast.onRetry && (
              <button
                onClick={() => {
                  toast.onRetry?.()
                  dismiss(toast.id)
                }}
                className="rounded-md bg-violet-500 px-2.5 py-1 text-xs font-medium text-white hover:bg-violet-400"
              >
                {t('common.retry')}
              </button>
            )}
            <button onClick={() => dismiss(toast.id)} className="text-zinc-500 hover:text-zinc-300">
              ×
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}
