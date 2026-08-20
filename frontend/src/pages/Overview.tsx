import type { ChangeEvent, CSSProperties } from 'react'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import {
  useActivities,
  useCapture,
  useDashboard,
  useEvidenceGroups,
  useFichas,
  useJobs,
  useJobEvents,
  useSchedules,
  useCreateSchedule,
  useSetScheduleEnabled,
  useReports,
  useSetActiveFicha,
  useSetupStatus,
  useSyncFichas,
  useTargets,
} from '../hooks/api'
import {
  confidenceFor,
  formatDate,
  friendlyJobMessage,
  friendlyJobStatus,
  friendlyJobType,
  jobStatusClass,
} from '../lib/format'
import { Icon } from '../components/Icon'
import { PageError, PageSkeleton } from '../components/AsyncState'
import { useToast } from '../hooks/useToast'
import { friendlyError } from '../lib/friendlyError'
import { reportDownloadUrl } from '../api/client'
import type { DashboardCategory, EvidenceGroup, Job, Report, Schedule } from '../types'
import { RouteDiscoveryAction } from '../components/RouteDiscoveryAction'

type BarStyle = CSSProperties & { '--bar-height'?: string }

function clamp(value: number, min: number, max: number) {
  return Math.max(min, Math.min(max, value))
}

function JobEntry({ job }: { job: Job }) {
  const progress = clamp(Number(job.progress) || 0, 0, 100)
  const status = friendlyJobStatus(job.status)
  const statusClassName = jobStatusClass(job.status)
  const isRunning = job.status === 'running'
  const isWaiting = job.status === 'waiting_user' || job.status === 'retrying'
  const chipClass = `status-chip ${statusClassName}${isWaiting ? ' waiting-pulse' : ''}`
  return (
    <article className="job">
      <div className="job-top">
        <strong>{friendlyJobType(job.type)}</strong>
        <span className={chipClass}>
          {isRunning && <i className="live-pulse" aria-hidden="true" />}
          {status}
        </span>
      </div>
      <small>
        {friendlyJobMessage(job.message || job.stage)} · {progress}%
      </small>
      <div className={`progress${isRunning ? ' running' : ''}`}>
        <i style={{ width: `${progress}%` }} />
      </div>
    </article>
  )
}

function EvidenceGroupCard({ group }: { group: EvidenceGroup }) {
  const confidence = confidenceFor({
    captureConfidence: undefined,
    confidence: group.confidence,
    evidenceCount: group.evidences?.length ?? 0,
  })
  const title =
    group.title || (group.itemCodes?.length ? `Ítems ${group.itemCodes.join(', ')}` : 'Grupo de evidencias')
  return (
    <article className="evidence-group-card">
      <div className="evidence-group-top">
        <h4>{title}</h4>
      </div>
      {group.reason && <p className="group-reason">{group.reason}</p>}
      <div className="evidence-group-foot">
        <span className="helper">{group.evidences?.length ?? 0} archivo(s)</span>
        <span className={`confidence ${confidence.key}`}>{confidence.label}</span>
      </div>
    </article>
  )
}

function ReportRow({ report }: { report: Report }) {
  return (
    <div className="report-row">
      <div className="report-row-copy">
        <span className="report-icon">
          <Icon name="report" size={16} />
        </span>
        <div>
          <strong>{report.name || 'Reporte'}</strong>
          <small>
            {(report.format || '—').toUpperCase()} · {formatDate(report.updatedAt || report.createdAt)}
          </small>
        </div>
      </div>
      <div className="inline">
        <span className="badge">{report.status}</span>
        {report.status === 'completed' ? (
          <a className="button ghost small" href={reportDownloadUrl(report.id)} target="_blank" rel="noreferrer">
            Descargar
          </a>
        ) : null}
      </div>
    </div>
  )
}

function ScheduleRow({ schedule, onToggle, isUpdating }: { schedule: Schedule; onToggle: () => void; isUpdating: boolean }) {
  const label = schedule.workerType === 'sync-fichas' ? 'Actualizar fichas' : schedule.workerType === 'capture-checklist' ? 'Preparar evidencias' : 'Proceso programado'
  const interval = schedule.intervalSeconds >= 86400 ? `Cada ${Math.round(schedule.intervalSeconds / 86400)} día(s)` : `Cada ${Math.max(1, Math.round(schedule.intervalSeconds / 3600))} hora(s)`
  return (
    <div className="schedule-row">
      <span className={`schedule-indicator ${schedule.enabled ? 'on' : ''}`} aria-hidden="true" />
      <div className="schedule-copy">
        <strong>{label}</strong>
        <small>{interval} · próxima ejecución {formatDate(schedule.nextRunAt)}</small>
      </div>
      <button className="toggle-button" aria-pressed={schedule.enabled} onClick={onToggle} disabled={isUpdating}>
        {schedule.enabled ? 'Activa' : 'Pausada'}
      </button>
    </div>
  )
}

export function Overview() {
  const navigate = useNavigate()
  const toast = useToast()

  const dashboardQuery = useDashboard()
  const fichasQuery = useFichas()
  const jobsQuery = useJobs()
  const setupQuery = useSetupStatus()
  const schedulesQuery = useSchedules()

  const syncFichas = useSyncFichas()
  const setActiveFicha = useSetActiveFicha()
  const capture = useCapture()
  const createSchedule = useCreateSchedule()
  const setScheduleEnabled = useSetScheduleEnabled()

  const dashboard = dashboardQuery.data
  const fichas = fichasQuery.data || []
  const jobs = jobsQuery.data || []
  const currentJob = jobs.find((job) => ['queued', 'running', 'waiting_user', 'retrying'].includes(job.status))
  const currentJobEventsQuery = useJobEvents(currentJob?.id)
  const schedules = schedulesQuery.data || []
  const [scheduleOpen, setScheduleOpen] = useState(false)
  const [scheduleWorker, setScheduleWorker] = useState<'sync-fichas' | 'capture-checklist'>('sync-fichas')
  const [scheduleInterval, setScheduleInterval] = useState(86400)

  const targetsQuery = useTargets(dashboard?.activeFichaId)
  const activitiesQuery = useActivities(dashboard?.activeFichaId)
  const evidenceGroupsQuery = useEvidenceGroups(dashboard?.activeFichaId)
  const reportsQuery = useReports()

  if (dashboardQuery.isLoading || fichasQuery.isLoading) {
    return <PageSkeleton label="Cargando resumen" />
  }

  if (dashboardQuery.isError) {
    if (!fichas.length) {
      return (
        <section className="card onboarding-card">
          <div className="card-pad">
            <div className="eyebrow">Primeros pasos</div>
            <h2 style={{ marginTop: 7 }}>Tu cuenta ya está guardada</h2>
            <p className="helper" style={{ marginTop: 8 }}>
              Ahora sincroniza tus fichas para elegir un curso. Después podrás buscar sus rutas y preparar evidencias sin volver a esta pantalla de configuración.
            </p>
            <div className="onboarding-steps" aria-label="Flujo recomendado">
              <span className="onboarding-step active"><b>1</b><strong>Sincronizar fichas</strong><small>Traer tus cursos de Zajuna</small></span>
              <span className="onboarding-step"><b>2</b><strong>Buscar rutas</strong><small>Encontrar las secciones del curso</small></span>
              <span className="onboarding-step"><b>3</b><strong>Preparar evidencias</strong><small>Capturar y revisar resultados</small></span>
            </div>
            <button
              className="button primary"
              type="button"
              onClick={() => syncFichas.mutate({ username: setupQuery.data?.zajunaUsername || '', documentType: setupQuery.data?.zajunaDocumentType || 'CC' }, { onSuccess: () => toast('Estamos sincronizando tus fichas.'), onError: (error) => toast(friendlyError(error.message), true) })}
              disabled={syncFichas.isPending}
            >
              {syncFichas.isPending ? 'Sincronizando…' : 'Sincronizar fichas'}
            </button>
            {jobs.some((job) => job.type === 'sync-fichas' && ['queued', 'running', 'retrying'].includes(job.status)) ? <p className="helper" style={{ marginTop: 10 }}>La sincronización está en curso. Puedes abrir Trabajos para ver el avance.</p> : null}
          </div>
        </section>
      )
    }
    return <PageError message="No pudimos cargar el resumen de la ficha activa." action={<button className="button" onClick={() => dashboardQuery.refetch()}>Reintentar</button>} />
  }

  if (!dashboard || !dashboard.ficha || !dashboard.activeFichaId) {
    return (
      <section className="card onboarding-card">
        <div className="card-pad">
          <div className="eyebrow">Siguiente paso</div>
          <h2 style={{ marginTop: 7 }}>Elige una ficha para comenzar</h2>
          <p className="helper" style={{ marginTop: 8 }}>La cuenta está lista. Selecciona una ficha sincronizada y después busca las rutas del curso.</p>
          <Link className="button primary" to="/fichas" style={{ marginTop: 18 }}>Ver mis fichas</Link>
        </div>
      </section>
    )
  }

  const activeFichaId = dashboard.activeFichaId

  const summary = dashboard.summary || { yes: 0, no: 0, pending: 0, percentage: 0 }
  const items = dashboard.items || []
  const done = Number(summary.yes) || 0
  const failed = Number(summary.no) || 0
  const pending = Number(summary.pending) || 0
  const total = Math.max(items.length, done + failed + pending, 1)
  const progress = clamp(Number(summary.percentage) || 0, 0, 100)
  const evidenceCount = items.reduce((sum, item) => sum + (Number(item.evidenceCount) || 0), 0)
  const routeCount = Number(targetsQuery.data?.summary?.slotCount || targetsQuery.data?.targets?.length || 0)
  const selectedActivityCount = activitiesQuery.data?.selectedCount || 0

  const jobProgress = currentJob ? clamp(Number(currentJob.progress) || 0, 0, 100) : 0

  const attentionItems = items.filter((entry) => entry.status === 'NO').slice(0, 3)
  const attentionJobs = jobs.filter((job) => job.status === 'failed').slice(0, 2)
  const attentionTotal =
    items.filter((entry) => entry.status === 'NO').length + jobs.filter((job) => job.status === 'failed').length

  const categories: DashboardCategory[] = dashboard.categories || []
  const bars = categories.map((category) => ({
    code: category.code,
    label: category.label || category.code || 'Categoría',
    total: Number(category.total) || 0,
    yes: Number(category.yes) || 0,
    no: Number(category.no) || 0,
    pending: Number(category.pending) || 0,
    value: clamp(Math.round(((Number(category.yes) || 0) / Math.max(Number(category.total) || 1, 1)) * 100), 0, 100),
  }))

  function handleSync() {
    syncFichas.mutate(
      { username: setupQuery.data?.zajunaUsername || '', documentType: setupQuery.data?.zajunaDocumentType || 'CC' },
      {
        onSuccess: () => toast('Estamos actualizando tus fichas.'),
        onError: (error) => toast(friendlyError(error.message), true),
      },
    )
  }

  function handleFichaChange(event: ChangeEvent<HTMLSelectElement>) {
    const value = event.target.value
    if (!value) return
    setActiveFicha.mutate(value, {
      onError: (error) => toast(friendlyError(error.message), true),
    })
  }

  function handleCapture() {
    if (!dashboard) return
    if (routeCount === 0) {
      toast('Primero busca las rutas de esta ficha.', true)
      return
    }
    capture.mutate(
      {
        fichaId: dashboard.activeFichaId,
        username: setupQuery.data?.zajunaUsername || '',
        documentType: setupQuery.data?.zajunaDocumentType || 'CC',
      },
      {
        onSuccess: () => toast('Estamos preparando tus evidencias.'),
        onError: (error) => toast(friendlyError(error.message), true),
      },
    )
  }

  function handleRefresh() {
    dashboardQuery.refetch()
    jobsQuery.refetch()
  }

  function handleCreateSchedule() {
    const username = setupQuery.data?.zajunaUsername || ''
    const documentType = setupQuery.data?.zajunaDocumentType || 'CC'
    const input = scheduleWorker === 'sync-fichas'
      ? { username, documentType }
      : { fichaId: activeFichaId, username, documentType }
    createSchedule.mutate(
      { workerType: scheduleWorker, input, intervalSeconds: scheduleInterval, enabled: true },
      {
        onSuccess: () => {
          toast('Programación guardada.')
          setScheduleOpen(false)
        },
        onError: (error) => toast(friendlyError(error.message), true),
      },
    )
  }

  function handleToggleSchedule(schedule: Schedule) {
    setScheduleEnabled.mutate(
      { id: schedule.id, enabled: !schedule.enabled },
      { onError: (error) => toast(friendlyError(error.message), true) },
    )
  }

  return (
    <div className="overview-workspace">
      <div className="overview-title-row">
        <div>
          <h2>Resumen de operación</h2>
          <p>
            Última sincronización con Zajuna: {formatDate(dashboard.ficha.updatedAt)} · {fichas.length} fichas
            locales
          </p>
        </div>
        <div className="overview-tools">
          <div className="ficha-control-wrap">
            <span className="ficha-control-label">Ficha</span>
            <select
              className="ficha-control"
              id="ficha-select"
              aria-label="Seleccionar ficha"
              value={dashboard.activeFichaId}
              onChange={handleFichaChange}
            >
              <option value="">Seleccionar ficha</option>
              {fichas.map((ficha) => (
                <option key={ficha.id} value={ficha.id}>
                  {ficha.externalId} · {ficha.name}
                </option>
              ))}
            </select>
          </div>
          <button className="button primary" onClick={handleSync} disabled={syncFichas.isPending}>
            Sincronizar fichas
          </button>
        </div>
      </div>

      <div className="metric-grid">
        <article className="metric-card">
          <div className="metric-label">Cumplimiento del checklist</div>
          <div className="metric-value-row">
            <strong className="metric-value">{progress}</strong>
            <span className="metric-unit">%</span>
          </div>
          <div className="metric-progress">
            <i style={{ width: `${progress}%` }} />
          </div>
          <div className="metric-note">
            {done} de {total} ítems cumplidos
          </div>
        </article>

        <article className="metric-card">
          <div className="metric-label">Evidencias capturadas</div>
          <div className="metric-value-row">
            <strong className="metric-value">{evidenceCount}</strong>
            {evidenceCount > 0 && <span className="metric-pill">guardadas</span>}
          </div>
          <div className="mini-bars">
            <i style={{ height: '35%' }} />
            <i style={{ height: '55%' }} />
            <i style={{ height: '42%' }} />
            <i style={{ height: '70%' }} />
            <i style={{ height: '60%' }} />
            <i style={{ height: '88%' }} />
            <i style={{ height: '100%' }} />
          </div>
          <div className="metric-note">Archivos relacionados con esta ficha</div>
        </article>

        <article className="metric-card">
          <div className="metric-label">Actividades seleccionadas</div>
          <div className="metric-value-row">
            <strong className="metric-value">{selectedActivityCount}</strong>
            <span className="metric-pill">a mi cargo</span>
          </div>
          <div className="metric-tags">
            <span className="metric-tag">Seleccion del profesor</span>
            <span className="metric-tag">{routeCount > 0 ? 'Mapa disponible' : 'Mapa pendiente'}</span>
          </div>
          <div className="metric-note">Alcance de trabajo · curso {dashboard.ficha.courseId || 'local'}</div>
        </article>

        <article className="metric-card focused">
          <div className="metric-label-row">
            <div className="metric-label">Trabajo en curso</div>
            <span className="metric-focused-badge">Tarjeta enfocada</span>
          </div>
          <div className="metric-value-row">
            <strong className="metric-value">{currentJob ? '1' : '0'}</strong>
            <span className="metric-note" style={{ margin: 0 }}>
              {currentJob ? friendlyJobType(currentJob.type) : 'Sin procesos activos'}
            </span>
          </div>
          <div className={`metric-progress${currentJob?.status === 'running' ? ' running' : ''}`}>
            <i style={{ width: `${jobProgress}%` }} />
          </div>
          <div className="metric-note">
            {currentJob ? `${jobProgress}% · ${friendlyJobStatus(currentJob.status)}` : 'La aplicación está lista para trabajar'}
          </div>
        </article>
      </div>

      {currentJob && (
        <section className="card active-work-card" aria-live="polite">
          <div className="active-work-copy">
            <div className="eyebrow">Proceso en ejecución</div>
            <h3>{friendlyJobType(currentJob.type)}</h3>
            <p className="helper">La aplicación está trabajando en segundo plano. Puedes continuar revisando la ficha mientras termina.</p>
            <JobEntry job={currentJob} />
          </div>
          <div className="active-work-events">
            <div className="side-title">
              <strong>Últimos avances</strong>
              <Link className="button ghost small" to={`/trabajos/${encodeURIComponent(currentJob.id)}`}>Ver detalles</Link>
            </div>
            {currentJobEventsQuery.data?.length ? (
              <ol className="mini-job-timeline">
                {currentJobEventsQuery.data.slice(-3).map((event, index) => (
                  <li key={`${event.createdAt}-${index}`}>
                    <span aria-hidden="true" />
                    <div><strong>{event.stage || 'Actualización'}</strong><small>{friendlyJobMessage(event.message)}</small></div>
                  </li>
                ))}
              </ol>
            ) : <p className="helper">Esperando el primer avance del proceso.</p>}
          </div>
        </section>
      )}

      <div className="overview-columns">
        <div className="overview-main">
          <section className="card checklist-overview">
            <div className="checklist-head">
              <div>
                <h3>Estado de los {total} ítems</h3>
                <p className="helper">Ficha {dashboard.ficha.externalId} · revisión de evidencias</p>
              </div>
              <button className="button ghost small success" onClick={() => navigate('/checklist')}>
                Abrir checklist
              </button>
            </div>
            <div className="segmented-progress" role="img" aria-label={`Cumplimiento: ${done} cumplidas, ${failed} no cumplidas y ${pending} pendientes de ${total}`}>
              <span className="done grow-in" style={{ width: `${(done / total) * 100}%` }}>
                {done ? `${done} cumplidas` : ''}
              </span>
              <span className="failed grow-in" style={{ width: `${(failed / total) * 100}%` }}>
                {failed || ''}
              </span>
              <span className="pending grow-in" style={{ width: `${(pending / total) * 100}%` }}>
                {pending ? `${pending} pendientes` : ''}
              </span>
            </div>
            <div className="legend-row">
              <span>
                <i style={{ background: 'var(--brand)' }} />
                Cumplidas {done}
              </span>
              <span>
                <i style={{ background: 'var(--no)' }} />
                No cumplidas {failed}
              </span>
              <span>
                <i style={{ background: '#f0c77e' }} />
                Pendientes {pending}
              </span>
            </div>
            <div className="category-summary">
              <div className="category-summary-title">Cumplimiento por categoría</div>
              <div className="category-bars legacy-category-bars" aria-hidden="true">
                {bars.length ? (
                  bars.map((bar) => (
                    <span key={bar.code} title={`${bar.label}: ${bar.yes} de ${bar.total} (${bar.value}%)`}>
                      <i role="img" aria-label={`${bar.label}: ${bar.yes} de ${bar.total}, ${bar.value}%`}>
                      <b className="grow-in" style={{ '--bar-height': `${bar.value}%` } as BarStyle} />
                      </i>
                      <small>{String(bar.code || '').replace(/^0+/, '') || '0'}</small>
                    </span>
                  ))
                ) : (
                  <span>
                    <i>
                      <b style={{ '--bar-height': '8%' } as BarStyle} />
                    </i>
                    <small>—</small>
                  </span>
                )}
              </div>
            <div className="category-grid" aria-label="Estado de cumplimiento por categoria">
                {bars.length ? bars.map((bar) => {
                  const status = bar.yes === bar.total ? 'Completa' : bar.no > 0 ? 'Requiere revision' : 'Pendiente'
                  const statusClass = bar.yes === bar.total ? 'complete' : bar.no > 0 ? 'attention' : 'pending'
                  return (
                    <button
                      key={bar.code}
                      type="button"
                      className={`category-card ${statusClass}`}
                      title={`Abrir ${bar.label} en el checklist`}
                      onClick={() => navigate(`/checklist?category=${encodeURIComponent(bar.code)}`)}
                    >
                      <span className="category-card-head">
                        <strong>{bar.code}</strong>
                        <span className="category-card-status">{status}</span>
                      </span>
                      <span className="category-card-label">{bar.label}</span>
                      <span className="category-card-progress" aria-hidden="true">
                        <i style={{ width: `${bar.value}%` }} />
                      </span>
                      <span className="category-card-meta">{bar.yes} de {bar.total} cumplidas · {bar.pending} pendientes</span>
                    </button>
                  )
                }) : <div className="empty">Todavia no hay categorias disponibles.</div>}
              </div>
              <div className="category-legend legacy-category-legend" aria-hidden="true">
                {categories.map((category) => category.label || category.code || '').join(' · ')}
              </div>
            </div>
          </section>

          {attentionTotal > 0 && (
            <section className="card attention-card">
              <div className="card-pad">
                <div className="side-title">
                  <h3>Requiere tu atención</h3>
                  <span className="badge alert">
                    {attentionTotal} asunto{attentionTotal === 1 ? '' : 's'}
                  </span>
                </div>
                <div className="attention-list">
                  {attentionItems.map((entry) => (
                    <div className="attention-row" key={entry.itemCode}>
                      <span className="attention-icon no">
                        <Icon name="warning" size={14} />
                      </span>
                      <div className="attention-copy">
                        <strong>
                          {entry.itemCode} · {entry.description}
                        </strong>
                        <span>{entry.categoryLabel || ''}</span>
                      </div>
                      <button className="button ghost small" onClick={() => navigate('/checklist')}>
                        Revisar
                      </button>
                    </div>
                  ))}
                  {attentionJobs.map((job) => (
                    <div className="attention-row" key={job.id}>
                      <span className="attention-icon warn">
                        <Icon name="warning" size={14} />
                      </span>
                      <div className="attention-copy">
                        <strong>{friendlyJobType(job.type)}</strong>
                        <span>{friendlyJobMessage(job.message || job.stage)}</span>
                      </div>
                      <button className="button ghost small" onClick={() => navigate('/trabajos')}>
                        Ver
                      </button>
                    </div>
                  ))}
                </div>
              </div>
            </section>
          )}
        </div>

        <aside className="overview-side">
          <section className="focus-card">
            <div className="eyebrow">Ficha activa</div>
            <h3 style={{ marginTop: 7 }}>{dashboard.ficha.name}</h3>
            <p className="helper" style={{ marginTop: 6 }}>
              Ficha {dashboard.ficha.externalId} · curso {dashboard.ficha.courseId}
            </p>
            <div className="job">
              {currentJob ? (
                <>
                  <JobEntry job={currentJob} />
                  {currentJobEventsQuery.data?.length ? (
                    <ol className="mini-job-timeline">
                      {currentJobEventsQuery.data.slice(-3).map((event, index) => (
                        <li key={`${event.createdAt}-${index}`}>
                          <span aria-hidden="true" />
                          <div><strong>{event.stage || 'Actualización'}</strong><small>{friendlyJobMessage(event.message)}</small></div>
                        </li>
                      ))}
                    </ol>
                  ) : null}
                  <Link className="button ghost small" style={{ marginTop: 10 }} to={`/trabajos/${encodeURIComponent(currentJob.id)}`}>
                    Ver timeline completo
                  </Link>
                </>
              ) : (
                <>
                  <strong>Todo listo para continuar</strong>
                  <small>Revisa las actividades y prepara las evidencias cuando quieras.</small>
                </>
              )}
            </div>
            <RouteDiscoveryAction compact variant="primary" label="Buscar rutas" />
            <button
              className="button"
              style={{ width: '100%', marginTop: 16 }}
              onClick={handleCapture}
              disabled={capture.isPending || routeCount === 0}
            >
              Preparar evidencias
            </button>
            {routeCount === 0 ? <p className="helper" style={{ marginTop: 8 }}>Busca las rutas del curso antes de preparar evidencias.</p> : null}
          </section>

          <section className="card schedule-card">
            <div className="card-pad">
              <div className="side-title">
                <div>
                  <h3>Programación local</h3>
                  <p className="helper" style={{ marginTop: 5 }}>Los procesos se ejecutan en este equipo, aunque cierres esta ventana.</p>
                </div>
                <button className="button ghost small" onClick={() => setScheduleOpen((open) => !open)}>
                  {scheduleOpen ? 'Cerrar' : 'Nueva'}
                </button>
              </div>
              {scheduleOpen ? (
                <div className="schedule-form">
                  <label>
                    <span>Proceso</span>
                    <select value={scheduleWorker} onChange={(event) => setScheduleWorker(event.target.value as typeof scheduleWorker)}>
                      <option value="sync-fichas">Actualizar fichas</option>
                      <option value="capture-checklist">Preparar evidencias</option>
                    </select>
                  </label>
                  <label>
                    <span>Frecuencia</span>
                    <select value={scheduleInterval} onChange={(event) => setScheduleInterval(Number(event.target.value))}>
                      <option value={3600}>Cada hora</option>
                      <option value={21600}>Cada 6 horas</option>
                      <option value={86400}>Cada día</option>
                      <option value={604800}>Cada semana</option>
                    </select>
                  </label>
                  <button className="button primary small" onClick={handleCreateSchedule} disabled={createSchedule.isPending}>
                    {createSchedule.isPending ? 'Guardando…' : 'Guardar programación'}
                  </button>
                </div>
              ) : null}
              <div className="schedule-list">
                {schedulesQuery.isError ? <div className="empty" role="alert">No pudimos cargar la programación local.</div> : schedules.length ? schedules.slice(0, 4).map((schedule) => (
                  <ScheduleRow key={schedule.id} schedule={schedule} onToggle={() => handleToggleSchedule(schedule)} isUpdating={setScheduleEnabled.isPending} />
                )) : <div className="empty">Todavía no hay procesos programados.</div>}
              </div>
            </div>
          </section>

          <section className="card">
            <div className="card-pad">
              <div className="side-title">
                <h3>Últimas acciones</h3>
                <button className="button ghost small" onClick={handleRefresh}>
                  Actualizar
                </button>
              </div>
              <div id="overview-jobs">
                {jobsQuery.isError ? <div className="empty" role="alert">No pudimos actualizar el historial de trabajos.</div> : jobs.length ? (
                  jobs.slice(0, 4).map((job) => <JobEntry key={job.id} job={job} />)
                ) : (
                  <div className="empty">Todavía no hay procesos.</div>
                )}
              </div>
            </div>
          </section>
        </aside>
      </div>

      <div className="grid two-col">
        <section className="card evidence-gallery">
          <div className="card-pad">
            <div className="side-title">
              <h3>Evidencias</h3>
              <span className="badge">{evidenceGroupsQuery.data?.length ?? 0} grupos</span>
            </div>
            {evidenceGroupsQuery.isError ? (
              <div className="empty" role="alert">No pudimos cargar los grupos de evidencias. La información principal sigue disponible.</div>
            ) : evidenceGroupsQuery.data && evidenceGroupsQuery.data.length ? (
              <div className="evidence-gallery-grid">
                {evidenceGroupsQuery.data.slice(0, 6).map((group, index) => (
                  <EvidenceGroupCard key={group.id ?? index} group={group} />
                ))}
              </div>
            ) : (
              <div className="empty">Todavía no hay evidencias agrupadas.</div>
            )}
          </div>
        </section>

        <section className="card reports-card">
          <div className="card-pad">
            <div className="side-title">
              <h3>Reportes recientes</h3>
              <button className="button ghost small" onClick={() => navigate('/reportes')}>
                Ver todos
              </button>
            </div>
            {reportsQuery.isError ? (
              <div className="empty" role="alert">No pudimos cargar los reportes recientes.</div>
            ) : reportsQuery.data && reportsQuery.data.length ? (
              <div className="report-list">
                {reportsQuery.data.slice(0, 5).map((report) => (
                  <ReportRow key={report.id} report={report} />
                ))}
              </div>
            ) : (
              <div className="empty">Todavía no hay reportes generados.</div>
            )}
          </div>
        </section>
      </div>
    </div>
  )
}
