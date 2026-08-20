import { Link, useNavigate, useParams } from 'react-router-dom'
import { PageError, PageSkeleton } from '../components/AsyncState'
import { useCancelJob, useJob, useJobEvents } from '../hooks/api'
import { useToast } from '../hooks/useToast'
import { friendlyError } from '../lib/friendlyError'
import {
  formatDate,
  friendlyJobMessage,
  friendlyJobStatus,
  friendlyJobType,
  jobStatusClass,
} from '../lib/format'
import type { JobEvent } from '../types'

function eventLabel(event: JobEvent) {
  const kind = String(event.kind || '').toLowerCase()
  if (kind.includes('error') || kind.includes('fail')) return 'Incidencia'
  if (kind.includes('wait')) return 'Revisión necesaria'
  if (kind.includes('complete') || kind.includes('finish')) return 'Resultado guardado'
  if (kind.includes('start') || kind.includes('begin')) return 'Proceso iniciado'
  return event.stage || 'Actualización del proceso'
}

function eventClass(event: JobEvent) {
  const kind = String(event.kind || '').toLowerCase()
  if (kind.includes('error') || kind.includes('fail')) return 'error'
  if (kind.includes('complete') || kind.includes('finish')) return 'ok'
  if (kind.includes('wait')) return 'warn'
  return 'running'
}

function JobEventRow({ event, index }: { event: JobEvent; index: number }) {
  const progress = Number.isFinite(Number(event.progress)) ? Math.max(0, Math.min(100, Number(event.progress))) : undefined
  return (
    <li className={`job-timeline-item ${eventClass(event)} rise-in`} style={{ animationDelay: `${index * 55}ms` }}>
      <span className="job-timeline-marker" aria-hidden="true" />
      <div className="job-timeline-copy">
        <div className="job-timeline-head">
          <strong>{eventLabel(event)}</strong>
          <time dateTime={event.createdAt}>{formatDate(event.createdAt)}</time>
        </div>
        <p>{friendlyJobMessage(event.message || event.stage)}</p>
        {progress !== undefined ? <span className="job-timeline-progress">{progress}%</span> : null}
      </div>
    </li>
  )
}

export function JobDetail() {
  const { jobId } = useParams<{ jobId: string }>()
  const navigate = useNavigate()
  const toast = useToast()
  const jobQuery = useJob(jobId)
  const job = jobQuery.data
  const eventsQuery = useJobEvents(jobId, !!job, job?.status)
  const cancelJob = useCancelJob()

  if (jobQuery.isLoading) return <PageSkeleton label="Cargando detalle del proceso" />
  if (jobQuery.isError || !job) {
    return (
      <PageError
        message="El proceso no existe o ya no está disponible en este equipo."
        action={
          <div className="inline">
            <button className="button" onClick={() => jobQuery.refetch()}>
              Reintentar
            </button>
            <button className="button ghost" onClick={() => navigate('/trabajos')}>
              Volver a trabajos
            </button>
          </div>
        }
      />
    )
  }

  const progress = Math.max(0, Math.min(100, Number(job.progress) || 0))
  const canCancel = ['queued', 'running', 'waiting_user', 'retrying'].includes(job.status)
  const events = eventsQuery.data || []

  function handleCancel() {
    if (!jobId || cancelJob.isPending) return
    cancelJob.mutate(jobId, {
      onSuccess: () => toast('Proceso cancelado.'),
      onError: (error) => toast(friendlyError(error.message), true),
    })
  }

  return (
    <div className="job-detail-layout">
      <div className="job-detail-back">
        <Link className="button ghost small" to="/trabajos">
          ← Todos los trabajos
        </Link>
      </div>

      <section className="card job-detail-hero">
        <div className="card-pad">
          <div className="job-detail-heading">
            <div>
              <div className="eyebrow">Detalle del proceso</div>
              <h2>{friendlyJobType(job.type)}</h2>
              <p className="helper" style={{ marginTop: 7 }}>
                Iniciado {formatDate(job.createdAt)} · Última actualización {formatDate(job.updatedAt)}
              </p>
            </div>
            <span className={`status-chip ${jobStatusClass(job.status)}${job.status === 'waiting_user' || job.status === 'retrying' ? ' waiting-pulse' : ''}`}>
              {job.status === 'running' ? <i className="live-pulse" aria-hidden="true" /> : null}
              {friendlyJobStatus(job.status)}
            </span>
          </div>

          <div className="job-detail-progress-row">
            <div className="job-detail-progress-track" aria-label={`Progreso ${progress}%`}>
              <i className={job.status === 'running' ? 'running' : ''} style={{ width: `${progress}%` }} />
            </div>
            <strong>{progress}%</strong>
          </div>
          <p className="job-detail-message">{friendlyJobMessage(job.message || job.stage)}</p>

          <div className="job-detail-meta">
            <span><b>Etapa</b>{job.stage || '—'}</span>
            <span><b>Intento</b>{job.attempt || 0} / {job.maxAttempts || 0}</span>
            <span><b>Finalizado</b>{formatDate(job.finishedAt)}</span>
          </div>

          {job.errorMessage ? <div className="job-detail-error" role="alert">{friendlyJobMessage(job.errorMessage)}</div> : null}

          <div className="job-detail-actions">
            {canCancel ? (
              <button className="button danger" onClick={handleCancel} disabled={cancelJob.isPending}>
                {cancelJob.isPending ? 'Cancelando…' : 'Cancelar proceso'}
              </button>
            ) : null}
            <button className="button ghost" onClick={() => jobQuery.refetch()} disabled={jobQuery.isFetching}>
              {jobQuery.isFetching ? 'Actualizando…' : 'Actualizar'}
            </button>
          </div>
        </div>
      </section>

      <section className="card job-timeline-card">
        <div className="card-pad">
          <div className="side-title">
            <div>
              <h3>Línea de tiempo</h3>
              <p className="helper" style={{ marginTop: 5 }}>
                Cada actualización queda registrada localmente para que puedas retomar el trabajo.
              </p>
            </div>
            {eventsQuery.isFetching ? <span className="badge">Actualizando</span> : <span className="badge">{events.length} eventos</span>}
          </div>

          {eventsQuery.isError ? (
            <div className="empty" role="status">No pudimos cargar los eventos. El estado principal sigue disponible.</div>
          ) : events.length ? (
            <ol className="job-timeline">
              {events.map((event, index) => <JobEventRow key={`${event.createdAt}-${index}`} event={event} index={index} />)}
            </ol>
          ) : (
            <div className="empty">Todavía no hay eventos detallados para este proceso.</div>
          )}
        </div>
      </section>
    </div>
  )
}
