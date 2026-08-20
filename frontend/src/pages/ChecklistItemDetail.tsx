import { Link, useParams } from 'react-router-dom'
import { PageError, PageSkeleton } from '../components/AsyncState'
import { evidenceDownloadUrl } from '../api/client'
import {
  useCapture,
  useChecklistItemDetail,
  useDashboard,
  useReviews,
  useSetItemStatus,
  useSetupStatus,
  useTargets,
} from '../hooks/api'
import { useToast } from '../hooks/useToast'
import { friendlyError } from '../lib/friendlyError'
import { confidenceFor, formatDate, routeStatusClass, routeStatusLabel } from '../lib/format'
import type { ItemStatus, RouteTarget } from '../types'

function targetLocation(target: RouteTarget) {
  return target.url || target.cssSelector || 'Ruta pendiente de definir'
}

export function ChecklistItemDetail() {
  const { itemCode } = useParams<{ itemCode: string }>()
  const decodedCode = itemCode ? decodeURIComponent(itemCode) : ''
  const toast = useToast()
  const dashboardQuery = useDashboard()
  const dashboard = dashboardQuery.data
  const detailQuery = useChecklistItemDetail(decodedCode, dashboard?.activeFichaId)
  const targetsQuery = useTargets(dashboard?.activeFichaId)
  const reviewsQuery = useReviews(dashboard?.activeFichaId)
  const setupQuery = useSetupStatus()
  const setStatus = useSetItemStatus()
  const capture = useCapture()

  if (dashboardQuery.isLoading) return <PageSkeleton label="Cargando detalle de tarea" />
  if (dashboardQuery.isError || !dashboard) {
    return <PageError message="No pudimos cargar el detalle de la ficha activa." action={<Link className="button" to="/checklist">Volver al checklist</Link>} />
  }

  if (detailQuery.isLoading) return <PageSkeleton label="Cargando historial de la tarea" />
  if (detailQuery.isError) {
    return <PageError message="No pudimos cargar el historial de esta tarea." action={<Link className="button" to="/checklist">Volver al checklist</Link>} />
  }

  const item = detailQuery.data?.item || dashboard.items.find((entry) => entry.itemCode === decodedCode)
  if (!item) {
    return <PageError message="No encontramos ese ítem en la ficha activa." action={<Link className="button" to="/checklist">Volver al checklist</Link>} />
  }

  const task = item
  const activeFichaId = dashboard.activeFichaId

  const targets = (targetsQuery.data?.targets || []).filter((target) => target.itemCode === task.itemCode)
  const reviews = reviewsQuery.data || []
  const confidence = confidenceFor(task)
  const maxEvidences = Math.max(1, Number(task.maxEvidences) || 1)
  const evidenceCount = Number(task.evidenceCount) || 0
  const events = detailQuery.data?.events || []

  function handleStatus(status: ItemStatus) {
    setStatus.mutate(
      { itemCode: task.itemCode, fichaId: activeFichaId, status },
      { onError: (error) => toast(friendlyError(error.message), true) },
    )
  }

  function handleCapture() {
    capture.mutate(
      {
        fichaId: activeFichaId,
        username: setupQuery.data?.zajunaUsername || '',
        documentType: setupQuery.data?.zajunaDocumentType || 'CC',
        itemCodes: [task.itemCode],
      },
      {
        onSuccess: () => toast('Estamos preparando la evidencia de esta tarea.'),
        onError: (error) => toast(friendlyError(error.message), true),
      },
    )
  }

  return (
    <div className="task-detail-layout">
      <div className="job-detail-back">
        <Link className="button ghost small" to="/checklist">
          ← Volver al checklist
        </Link>
      </div>

      <section className="card task-detail-hero">
        <div className="card-pad">
          <div className="task-detail-heading">
            <div>
              <div className="eyebrow">{task.categoryLabel || 'Tarea del checklist'}</div>
              <h2>{task.description}</h2>
              <p className="helper" style={{ marginTop: 7 }}>
                <span className="mono">{task.itemCode}</span> · ficha {dashboard.ficha.externalId}
              </p>
            </div>
            <span className={`confidence ${confidence.key}`} title={confidence.detail}>{confidence.label}</span>
          </div>

          <div className="task-detail-status-row">
            <div>
              <span className="eyebrow">Estado de revisión</span>
              <div className="status-seg-group" role="group" aria-label="Estado de la tarea" style={{ marginTop: 8 }}>
                {(['SI', 'NO', 'PENDIENTE'] as ItemStatus[]).map((status) => (
                  <button type="button" key={status} className={`status-seg ${status.toLowerCase()} ${task.status === status ? 'active' : ''}`} aria-pressed={task.status === status} onClick={() => handleStatus(status)} disabled={setStatus.isPending}>
                    {status === 'SI' ? 'Sí' : status === 'NO' ? 'No' : 'Pendiente'}
                  </button>
                ))}
              </div>
            </div>
            <div className="task-detail-slot-summary">
              <strong>{evidenceCount} / {maxEvidences}</strong>
              <span>evidencias guardadas</span>
            </div>
          </div>

          <div className="task-detail-actions">
            <button className="button primary" onClick={handleCapture} disabled={capture.isPending}>
              {capture.isPending ? 'Preparando…' : 'Capturar esta tarea'}
            </button>
            <Link className="button ghost" to="/evidencias">Ver galería de evidencias</Link>
          </div>
        </div>
      </section>

      <div className="task-detail-columns">
        <section className="card">
          <div className="card-pad">
            <div className="side-title">
              <div>
                <h3>Slots de evidencia</h3>
                <p className="helper" style={{ marginTop: 5 }}>Cada captura queda asociada a un espacio concreto de esta tarea.</p>
              </div>
              <span className="badge">{targets.length || maxEvidences} slots</span>
            </div>

            <div className="task-slot-list">
              {Array.from({ length: Math.max(targets.length, maxEvidences) }).map((_, index) => {
                const target = targets[index]
                const evidence = task.evidences?.find((entry) => (entry.slotNumber || 1) === index + 1)
                const review = target ? reviews.find((entry) => entry.routeKey === `${target.groupName}|${target.url || target.cssSelector || target.itemCode}`) : undefined
                return (
                <article className="task-slot-card" key={`${task.itemCode}-${index}`}>
                    <div className={`slot-dot${evidence ? ' filled' : ''}`} aria-hidden="true" />
                    <div className="task-slot-copy">
                      <strong>Slot {index + 1}</strong>
                      <span>{target ? target.name || target.groupName : 'Objetivo aún no asignado'}</span>
                      {target ? <small>{targetLocation(target)}</small> : null}
                    </div>
                    {evidence ? (
                      <a className="button ghost small" href={evidenceDownloadUrl(evidence.id)} target="_blank" rel="noreferrer">
                        Abrir evidencia
                      </a>
                    ) : (
                      <span className="badge">Pendiente</span>
                    )}
                    {review ? <span className={`status-chip ${routeStatusClass(review.status)}`}>{routeStatusLabel(review.status)}</span> : null}
                  </article>
                )
              })}
            </div>
          </div>
        </section>

        <section className="card">
          <div className="card-pad">
            <h3>Historial visible</h3>
            <p className="helper" style={{ marginTop: 5 }}>Última decisión y archivos disponibles para esta tarea.</p>
            <div className="task-history">
              {events.map((event) => (
                <div className="task-history-row" key={event.id}>
                  <span className={`diagnostic-icon ${event.toStatus === 'SI' ? 'ok' : event.toStatus === 'NO' ? 'error' : 'warn'}`} aria-hidden="true" />
                  <div>
                    <strong>{event.fromStatus ? `${event.fromStatus} → ${event.toStatus}` : `Estado inicial: ${event.toStatus}`}</strong>
                    <small>{event.source === 'manual' ? 'Decisión manual' : event.source} · {formatDate(event.createdAt)}</small>
                  </div>
                </div>
              ))}
              <div className="task-history-row">
                <span className={`diagnostic-icon ${task.status === 'SI' ? 'ok' : task.status === 'NO' ? 'error' : 'warn'}`} aria-hidden="true" />
                <div><strong>Estado actual: {task.status === 'SI' ? 'Sí' : task.status === 'NO' ? 'No' : 'Pendiente'}</strong><small>Actualizado {formatDate(task.updatedAt)}</small></div>
              </div>
              <div className="task-history-row">
                <span className="diagnostic-icon ok" aria-hidden="true" />
                <div><strong>{evidenceCount ? `${evidenceCount} evidencia(s) guardada(s)` : 'Aún no hay evidencias'}</strong><small>Máximo permitido: {maxEvidences}</small></div>
              </div>
              <div className="route-note">El historial conserva cada cambio manual de estado con fecha y origen. Los resultados de captura se consultan en el timeline del trabajo asociado.</div>
            </div>
          </div>
        </section>
      </div>
    </div>
  )
}
