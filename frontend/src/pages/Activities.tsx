import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { PageError, PageSkeleton } from '../components/AsyncState'
import { useActivities, useDashboard, useSaveActivities } from '../hooks/api'
import { useToast } from '../hooks/useToast'
import { friendlyError } from '../lib/friendlyError'
import type { Activity } from '../types'
import { RouteDiscoveryAction } from '../components/RouteDiscoveryAction'

function ActivityRow({
  activity,
  checked,
  onToggle,
}: {
  activity: Activity
  checked: boolean
  onToggle: (id: string) => void
}) {
  return (
    <label className="activity-row">
      <input
        type="checkbox"
        name="activity-id"
        value={activity.id}
        checked={checked}
        onChange={() => onToggle(activity.id)}
      />
      <span>
        <span className="activity-title">{activity.title || 'Actividad sin título'}</span>
        <span className="activity-meta">
          <span>{activity.technical ? 'Área técnica' : 'Por revisar'}</span>
          {activity.phaseName ? <span>{activity.phaseName}</span> : null}
          <span>Referencia {activity.id}</span>
        </span>
      </span>
      <span className="badge">{activity.technical ? 'Técnica' : 'Revisar'}</span>
    </label>
  )
}

export function Activities() {
  const toast = useToast()
  const dashboardQuery = useDashboard()
  const dashboard = dashboardQuery.data
  const activeFichaId = dashboard?.activeFichaId
  const activitiesQuery = useActivities(activeFichaId)
  const data = activitiesQuery.data
  const saveActivities = useSaveActivities()
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())

  useEffect(() => {
    if (!data) return
    setSelectedIds(new Set(data.activities.filter((activity) => activity.selected).map((activity) => activity.id)))
  }, [data])

  if (dashboardQuery.isLoading || activitiesQuery.isLoading) return <PageSkeleton label="Cargando actividades" />
  if (dashboardQuery.isError) return <PageError message="No pudimos cargar la ficha activa." action={<Link className="button" to="/fichas">Elegir una ficha</Link>} />
  if (!activeFichaId) {
    return (
      <section className="card onboarding-card">
        <div className="card-pad">
          <div className="eyebrow">Actividades</div>
          <h2 style={{ marginTop: 7 }}>Elige una ficha para continuar</h2>
          <p className="helper" style={{ marginTop: 8 }}>Las actividades se consultan por ficha. Sincroniza tus fichas y selecciona una antes de definir qué evidencias preparar.</p>
          <Link className="button primary" to="/fichas" style={{ marginTop: 18 }}>Ver fichas</Link>
        </div>
      </section>
    )
  }
  if (activitiesQuery.isError) return <PageError message="No pudimos cargar las actividades de esta ficha." action={<button className="button" onClick={() => activitiesQuery.refetch()}>Reintentar</button>} />
  if (!data) return <div className="empty">Todavía no hay una respuesta de actividades para esta ficha.</div>
  if (data.mapReady === false) {
    return (
      <section className="card onboarding-card">
        <div className="card-pad">
          <div className="eyebrow">Paso 2 · Buscar rutas</div>
          <h2 style={{ marginTop: 7 }}>Primero busca las rutas del curso</h2>
          <p className="helper" style={{ marginTop: 8 }}>{data.discovery?.message || 'Necesitamos leer el contenido del curso para mostrarte las actividades disponibles.'}</p>
          <div className="inline" style={{ marginTop: 18 }}>
            <RouteDiscoveryAction variant="primary" />
            <Link className="button ghost" to="/checklist">Ir al checklist</Link>
          </div>
        </div>
      </section>
    )
  }

  const activities = data.activities || []
  const selected = selectedIds.size

  const toggleActivity = (id: string) => {
    setSelectedIds((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const handleSave = () => {
    if (!activeFichaId) return
    const selectedActivityIds = Array.from(selectedIds)
    saveActivities.mutate(
      { fichaId: activeFichaId, selectedActivityIds },
      {
        onSuccess: () => {
          toast(
            selectedActivityIds.length
              ? `Guardamos ${selectedActivityIds.length} actividades a tu cargo.`
              : 'Selecciona al menos una actividad antes de preparar las evidencias.',
          )
        },
        onError: (error) => {
          toast(friendlyError(error.message), true)
        },
      },
    )
  }

  return (
    <div className="grid">
      <section className="card">
        <div className="card-pad">
          <div className="side-title">
            <div>
              <h3>Actividades a mi cargo</h3>
              <p className="helper" style={{ marginTop: 5 }}>
                Selecciona las actividades que corresponden a tu trabajo. Las fechas y evidencias se limitarán a esta
                selección.
              </p>
            </div>
            <span className="badge">{selected} seleccionadas</span>
          </div>
          {selected ? (
            <p className="helper">Solo se prepararán evidencias relacionadas con estas actividades.</p>
          ) : (
            <div className="activity-warning">Selecciona al menos una actividad antes de preparar las evidencias.</div>
          )}
          <div className="activity-list">
            {activities.length ? (
              activities.map((activity) => (
                <ActivityRow
                  key={activity.id}
                  activity={activity}
                  checked={selectedIds.has(activity.id)}
                  onToggle={toggleActivity}
                />
              ))
            ) : (
              <div className="empty">Todavía no hay actividades disponibles. Actualiza el contenido del curso para buscarlas.</div>
            )}
          </div>
          <div className="activity-actions">
            <span className="helper">{activities.length} actividades encontradas</span>
            <button className="button small" onClick={handleSave} disabled={saveActivities.isPending}>
              {saveActivities.isPending ? 'Procesando…' : 'Guardar selección'}
            </button>
          </div>
        </div>
      </section>
    </div>
  )
}
