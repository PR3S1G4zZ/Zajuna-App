import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import '@fontsource/inter/latin-ext-400.css'
import '@fontsource/inter/latin-ext-500.css'
import '@fontsource/inter/latin-ext-600.css'
import '@fontsource/inter/latin-ext-700.css'
import '@fontsource/plus-jakarta-sans/latin-ext-400.css'
import '@fontsource/plus-jakarta-sans/latin-ext-500.css'
import '@fontsource/plus-jakarta-sans/latin-ext-600.css'
import '@fontsource/plus-jakarta-sans/latin-ext-700.css'
import '@fontsource/plus-jakarta-sans/latin-ext-800.css'
import '@fontsource/ibm-plex-mono/latin-ext-400.css'
import '@fontsource/ibm-plex-mono/latin-ext-500.css'
import '@fontsource/ibm-plex-mono/latin-ext-600.css'
import '@fontsource/ibm-plex-mono/latin-ext-700.css'
import './index.css'
import App from './App.tsx'
import { ToastProvider } from './hooks/ToastProvider'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
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
