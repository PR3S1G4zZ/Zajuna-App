import { createContext } from 'react'

export interface ToastContextValue {
  toast: (message: string, error?: boolean) => void
}

export const ToastContext = createContext<ToastContextValue | null>(null)
