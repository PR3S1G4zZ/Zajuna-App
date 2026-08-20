import { Link } from 'react-router-dom'
import { useDiscoverCourseMaps, useJobs, useSetupStatus } from '../hooks/api'
import { friendlyJobStatus, friendlyJobType, jobStatusClass } from '../lib/format'
import { useToast } from '../hooks/useToast'
import { friendlyError } from '../lib/friendlyError'

interface RouteDiscoveryActionProps {
  variant?: 'primary' | 'ghost' | 'secondary'
  compact?: boolean
  className?: string
  label?: string
}

/**
 * Starts the route-map discovery job and keeps the prerequisite visible while it runs.
 * Capture must happen after this job completes; linking to the job makes that dependency
 * explicit instead of leaving the user with a backend error.
 */
export function RouteDiscoveryAction({
  variant = 'primary',
  compact = false,
  className = '',
  label = 'Buscar rutas',
}: RouteDiscoveryActionProps) {
  const toast = useToast()
  const { data: setup } = useSetupStatus()
  const jobsQuery = useJobs()
  const discover = useDiscoverCourseMaps()

  const discoverJobs = (jobsQuery.data || [])
    .filter((job) => job.type === 'discover-course-maps')
    .sort((a, b) => String(b.updatedAt || b.createdAt || '').localeCompare(String(a.updatedAt || a.createdAt || '')))
  const latest = discoverJobs[0]
  const active = latest && ['queued', 'running', 'waiting_user', 'retrying'].includes(latest.status) ? latest : undefined
  const buttonLabel = discover.isPending ? 'Enviando…' : active ? (latest.status === 'queued' ? 'En cola…' : 'Buscando rutas…') : label
  const classes = ['button', variant, compact ? 'small' : '', className].filter(Boolean).join(' ')

  function handleDiscover() {
    if (active || discover.isPending) return
    discover.mutate(
      {
        username: setup?.zajunaUsername || '',
        documentType: setup?.zajunaDocumentType || 'CC',
      },
      {
        onSuccess: () => toast('Buscaremos las rutas del curso. Puedes seguir trabajando mientras avanza.'),
        onError: (error) => toast(friendlyError(error.message), true),
      },
    )
  }

  return (
    <span className="route-discovery-action">
      <button type="button" className={classes} onClick={handleDiscover} disabled={!!active || discover.isPending}>
        {buttonLabel}
      </button>
      {latest && !active && latest.status === 'completed' ? (
        <span className="route-action-status ok" role="status">
          Rutas listas · <Link to={`/trabajos/${latest.id}`}>ver trabajo</Link>
        </span>
      ) : null}
      {latest && !active && latest.status === 'failed' ? (
        <span className="route-action-status error" role="status">
          No se pudo buscar · <Link to={`/trabajos/${latest.id}`}>ver detalle</Link>
        </span>
      ) : null}
      {active ? (
        <span className="route-action-status" role="status">
          <span className={`status-chip ${jobStatusClass(active.status)}`}>{friendlyJobStatus(active.status)}</span>{' '}
          {friendlyJobType(active.type)} · <Link to={`/trabajos/${active.id}`}>ver avance</Link>
        </span>
      ) : null}
    </span>
  )
}
