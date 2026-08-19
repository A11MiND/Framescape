import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import './index.css'
import './i18n'
import './lib/theme'
import App from './App.tsx'
import { ToastProvider } from './components/Toast.tsx'
import { ApiError } from './lib/api'

// No custom retry config here meant every failed query — including a 404
// for an asset the user had already soft-deleted — retried 3 times with
// exponential backoff (react-query's own default) before finally settling
// into an error state. A 404 will never succeed no matter how many times
// it's retried, so that's ~7s of a pulsing skeleton for something that was
// already a known, permanent "not found" on the very first try — read live
// as the page being stuck. 4xx (client errors: not found, bad request,
// forbidden — all permanent) skip retries entirely; everything else
// (network blips, 5xx) still gets react-query's normal retry behavior.
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

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </ToastProvider>
    </QueryClientProvider>
  </StrictMode>,
)
