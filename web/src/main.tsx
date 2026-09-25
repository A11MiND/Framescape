import { StrictMode, Suspense } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import './index.css'
import './i18n'
import './lib/theme'
import App from './app/App'
import { ToastProvider } from './components/Toast'
import { ApiError } from './lib/api/client'
import { StreamProvider } from './lib/stream/StreamProvider'
import { TooltipProvider } from './ui'
import { PageFallback } from './app/shell/AppLayout'

// Client errors (4xx) never succeed on retry; network errors and 5xx get
// the default retries.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (failureCount, error) => {
        if (error instanceof ApiError && error.status >= 400 && error.status < 500) return false
        return failureCount < 3
      },
    },
  },
})

// Demo mode serves sample data for screenshot review; the branch is removed
// from production builds.
if (import.meta.env.VITE_DEMO === '1') {
  const { installDemo } = await import('./demo/install')
  installDemo()
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <StreamProvider>
        <TooltipProvider>
          <ToastProvider>
            <BrowserRouter>
              <Suspense fallback={<PageFallback />}>
                <App />
              </Suspense>
            </BrowserRouter>
          </ToastProvider>
        </TooltipProvider>
      </StreamProvider>
    </QueryClientProvider>
  </StrictMode>,
)
