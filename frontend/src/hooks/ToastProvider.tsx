import { useCallback, useRef, useState, type ReactNode } from 'react'
import { ToastContext } from './toast-context'

interface ToastEntry {
  id: number
  message: string
  error: boolean
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [entries, setEntries] = useState<ToastEntry[]>([])
  const nextId = useRef(0)

  const toast = useCallback((message: string, error = false) => {
    const id = nextId.current++
    setEntries((current) => [...current, { id, message, error }])
    setTimeout(() => {
      setEntries((current) => current.filter((entry) => entry.id !== id))
    }, 4200)
  }, [])

  return (
    <ToastContext.Provider value={{ toast }}>
      {children}
      {entries.map((entry) => (
        <div key={entry.id} className={`toast${entry.error ? ' error' : ''}`} role={entry.error ? 'alert' : 'status'}>
          {entry.message}
        </div>
      ))}
    </ToastContext.Provider>
  )
}
