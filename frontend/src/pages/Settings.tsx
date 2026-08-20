import { useEffect, useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import { backupDownloadUrl } from '../api/client'
import { PageError, PageSkeleton } from '../components/AsyncState'
import { useBackups, useCleanupBackups, useCreateBackup, useDashboard, useDeleteBackup, useFichas, useRestoreBackup, useSaveSettings, useSaveSetup, useSettings, useSetupStatus } from '../hooks/api'
import { useToast } from '../hooks/useToast'
import { friendlyError } from '../lib/friendlyError'
import type { AppSettings } from '../types'

type SettingsTabId = 'account' | 'capture' | 'storage' | 'backup' | 'notifications' | 'about'
type DocumentType = 'CC' | 'TI' | 'CE'

const TABS: Array<{ id: SettingsTabId; label: string }> = [
  { id: 'account', label: 'Cuenta Zajuna' },
  { id: 'capture', label: 'Capturas' },
  { id: 'storage', label: 'Almacenamiento' },
  { id: 'backup', label: 'Copias de seguridad' },
  { id: 'notifications', label: 'Notificaciones' },
  { id: 'about', label: 'Acerca de' },
]

const DEFAULT_SETTINGS: AppSettings = {
  session: { autoRenew: true },
  capture: { fullPage: true, reuseSession: true, motion: true },
  notifications: { jobCompleted: true, needsReview: true },
  storage: { retentionKeep: 5, retentionDays: 30 },
}

function formatBytes(value: number) {
  if (!value) return '0 B'
  if (value < 1024 * 1024) return `${Math.max(1, Math.round(value / 1024))} KB`
  return `${(value / (1024 * 1024)).toFixed(1)} MB`
}

export function Settings() {
  const [searchParams, setSearchParams] = useSearchParams()
  const tabParam = searchParams.get('tab')
  const activeTab: SettingsTabId = TABS.some((tab) => tab.id === tabParam) ? (tabParam as SettingsTabId) : 'account'

  const setupQuery = useSetupStatus()
  const setup = setupQuery.data
  const { data: fichas } = useFichas()
  const { data: dashboard } = useDashboard()
  const { data: backups } = useBackups()
  const settingsQuery = useSettings()
  const settings = settingsQuery.data
  const saveSetup = useSaveSetup()
  const saveSettings = useSaveSettings()
  const createBackup = useCreateBackup()
  const deleteBackup = useDeleteBackup()
  const cleanupBackups = useCleanupBackups()
  const restoreBackup = useRestoreBackup()
  const toast = useToast()

  const [documentType, setDocumentType] = useState<DocumentType>(setup?.zajunaDocumentType || 'CC')
  const [username, setUsername] = useState(setup?.zajunaUsername || '')
  const [password, setPassword] = useState('')
  const [preferences, setPreferences] = useState<AppSettings>(DEFAULT_SETTINGS)

  useEffect(() => {
    if (setup) {
      setDocumentType(setup.zajunaDocumentType || 'CC')
      setUsername(setup.zajunaUsername || '')
    }
  }, [setup])

  useEffect(() => {
    if (settings) setPreferences(settings)
  }, [settings])

  if (setupQuery.isLoading || settingsQuery.isLoading) return <PageSkeleton label="Cargando configuración local" />
  if (setupQuery.isError || settingsQuery.isError) return <PageError message="No pudimos cargar las preferencias locales." action={<button className="button" onClick={() => { setupQuery.refetch(); settingsQuery.refetch() }}>Reintentar</button>} />

  function setTab(tab: SettingsTabId) {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        next.set('tab', tab)
        return next
      },
      { replace: true },
    )
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault()
    try {
      await saveSetup.mutateAsync({ zajunaUsername: username.trim(), zajunaDocumentType: documentType, zajunaPassword: password })
      toast('Conexión guardada correctamente.')
      setPassword('')
    } catch (err) {
      const message = err instanceof Error ? err.message : 'No se pudo guardar la conexión.'
      toast(friendlyError(message), true)
    }
  }

  async function handleBackup() {
    try {
      await createBackup.mutateAsync()
      toast('Copia de seguridad creada correctamente.')
    } catch (err) {
      const message = err instanceof Error ? err.message : 'No se pudo crear la copia de seguridad.'
      toast(friendlyError(message), true)
    }
  }

  async function handleDeleteBackup(name: string) {
    if (!window.confirm(`¿Eliminar la copia ${name}? Esta acción no se puede deshacer.`)) return
    try {
      await deleteBackup.mutateAsync(name)
      toast('Copia eliminada correctamente.')
    } catch (err) {
      const message = err instanceof Error ? err.message : 'No se pudo eliminar la copia de seguridad.'
      toast(friendlyError(message), true)
    }
  }

  async function handleRestoreBackup(name: string) {
    if (!window.confirm(`Se creará una copia de seguridad de protección y se preparará ${name} para restaurarla al reiniciar. ¿Continuar?`)) return
    try {
      const result = await restoreBackup.mutateAsync(name)
      toast(`Restauración preparada. Reinicia la aplicación para aplicar la copia; respaldo de protección: ${result.safetyBackup}.`)
    } catch (err) {
      const message = err instanceof Error ? err.message : 'No se pudo preparar la restauración.'
      toast(friendlyError(message), true)
    }
  }

  async function handleCleanupBackups() {
    const { retentionKeep, retentionDays } = preferences.storage
    if (!window.confirm(`Se conservarán las ${retentionKeep} copias más recientes y se eliminarán solo las de más de ${retentionDays} días. ¿Continuar?`)) return
    try {
      const result = await cleanupBackups.mutateAsync({ keep: retentionKeep, olderThanDays: retentionDays })
      toast(result.deleted.length ? `Se eliminaron ${result.deleted.length} copias antiguas.` : 'No hay copias antiguas para limpiar.')
    } catch (err) {
      const message = err instanceof Error ? err.message : 'No se pudieron limpiar las copias antiguas.'
      toast(friendlyError(message), true)
    }
  }

  async function updatePreferences(next: AppSettings) {
    const previous = preferences
    setPreferences(next)
    try {
      await saveSettings.mutateAsync(next)
    } catch (err) {
      setPreferences(previous)
      const message = err instanceof Error ? err.message : 'No se pudieron guardar las preferencias.'
      toast(friendlyError(message), true)
    }
  }

  function togglePreference(section: 'session' | 'capture' | 'notifications', key: string) {
    const current = preferences[section] as Record<string, boolean>
    updatePreferences({ ...preferences, [section]: { ...current, [key]: !current[key] } } as AppSettings)
  }

  function Toggle({ pressed, label, onClick }: { pressed: boolean; label: string; onClick: () => void }) {
    return (
      <button className={`toggle settings-toggle${pressed ? ' active' : ''}`} type="button" role="switch" aria-checked={pressed} aria-label={label} onClick={onClick} disabled={saveSettings.isPending}>
        <i />
      </button>
    )
  }

  const evidenceCount = (dashboard?.items ?? []).reduce((sum, item) => sum + (Number(item.evidenceCount) || 0), 0)

  return (
    <div className="settings-layout">
      <div className="settings-tabs" role="tablist">
        {TABS.map((tab) => (
          <button
            key={tab.id}
            type="button"
            className={`settings-tab${activeTab === tab.id ? ' active' : ''}`}
            role="tab"
            aria-selected={activeTab === tab.id}
            aria-controls={`settings-panel-${tab.id}`}
            id={`settings-tab-${tab.id}`}
            onKeyDown={(event) => {
              if (!['ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(event.key)) return
              event.preventDefault()
              const currentIndex = TABS.findIndex((entry) => entry.id === tab.id)
              const nextIndex = event.key === 'Home' ? 0 : event.key === 'End' ? TABS.length - 1 : (currentIndex + (event.key === 'ArrowRight' ? 1 : -1) + TABS.length) % TABS.length
              const nextTab = TABS[nextIndex]
              setTab(nextTab.id)
              document.getElementById(`settings-tab-${nextTab.id}`)?.focus()
            }}
            onClick={() => setTab(tab.id)}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {activeTab === 'account' && (
        <div id="settings-panel-account" className="settings-grid" role="tabpanel" aria-labelledby="settings-tab-account" tabIndex={0}>
          <section className="card settings-section">
            <div className="settings-section-head">
              <h3>Credenciales de Zajuna</h3>
              <p className="helper">Zajuna App no tiene una cuenta propia. Estas credenciales solo abren la sesión contra el campus.</p>
            </div>
            <div className="card-pad">
              <form onSubmit={handleSubmit}>
                <div className="field">
                  <label htmlFor="settings-document-type">Tipo de documento</label>
                  <select
                    id="settings-document-type"
                    name="documentType"
                    value={documentType}
                    onChange={(event) => setDocumentType(event.target.value as DocumentType)}
                  >
                    <option value="CC">Cédula de ciudadanía (CC)</option>
                    <option value="TI">Tarjeta de identidad (TI)</option>
                    <option value="CE">Cédula de extranjería (CE)</option>
                  </select>
                </div>
                <div className="field">
                  <label htmlFor="settings-username">Número de documento</label>
                  <input
                    id="settings-username"
                    name="username"
                    inputMode="numeric"
                    autoComplete="username"
                    value={username}
                    onChange={(event) => setUsername(event.target.value)}
                    required
                  />
                </div>
                <div className="field">
                  <label htmlFor="settings-password">Nueva contraseña de Zajuna</label>
                  <input
                    id="settings-password"
                    name="password"
                    type="password"
                    autoComplete="new-password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    required
                  />
                </div>
                <div className="form-actions">
                  <button className="button" type="submit" disabled={saveSetup.isPending}>
                    {saveSetup.isPending ? 'Guardando…' : 'Guardar conexión'}
                  </button>
                </div>
              </form>
            </div>
          </section>
          <div className="grid">
            <section className="card settings-section">
              <div className="settings-section-head">
                <h3>Sesión y automatización</h3>
                <p className="helper">La aplicación protege la sesión y la reutiliza durante los trabajos.</p>
              </div>
              <div className="settings-row">
                <div>
                  <strong>Renovar la sesión automáticamente</strong>
                  <span>Si la sesión caduca, se vuelve a autenticar sin detener la captura.</span>
                </div>
                <Toggle pressed={preferences.session.autoRenew} label="Renovar la sesión automáticamente" onClick={() => togglePreference('session', 'autoRenew')} />
              </div>
              <div className="settings-row">
                <div>
                  <strong>Estado de conexión</strong>
                  <span>{setup?.setupComplete ? 'Conexión configurada en este equipo.' : 'Pendiente de configurar.'}</span>
                </div>
                <span className={`status-chip ${setup?.setupComplete ? 'ok' : 'pending'}`}>
                  {setup?.setupComplete ? 'Verificada' : 'Pendiente'}
                </span>
              </div>
            </section>
          </div>
        </div>
      )}

      {activeTab === 'capture' && (
        <div id="settings-panel-capture" className="settings-grid" role="tabpanel" aria-labelledby="settings-tab-capture" tabIndex={0}>
          <section className="card settings-section">
            <div className="settings-section-head">
              <h3>Preferencias de captura</h3>
              <p className="helper">Estas opciones conservan la proporción de la maqueta y ayudan a que las evidencias sean comparables.</p>
            </div>
            <div className="settings-row">
              <div>
                <strong>Página completa</strong>
                <span>Captura toda la sección, incluyendo contenido desplegado.</span>
              </div>
              <Toggle pressed={preferences.capture.fullPage} label="Capturar página completa" onClick={() => togglePreference('capture', 'fullPage')} />
            </div>
            <div className="settings-row">
              <div>
                <strong>Sesión Chromium reutilizable</strong>
                <span>Reduce el tiempo entre capturas sin volver a iniciar sesión.</span>
              </div>
              <Toggle pressed={preferences.capture.reuseSession} label="Reutilizar la sesión de Chromium" onClick={() => togglePreference('capture', 'reuseSession')} />
            </div>
            <div className="settings-row">
              <div>
                <strong>Animaciones de carga</strong>
                <span>Muestra skeleton mientras llegan los datos locales o de Zajuna.</span>
              </div>
              <Toggle pressed={preferences.capture.motion} label="Mostrar animaciones de carga" onClick={() => togglePreference('capture', 'motion')} />
            </div>
          </section>
          <section className="card settings-section">
            <div className="settings-section-head">
              <h3>Recomendación</h3>
              <p className="helper">Mantén esta configuración para conservar evidencias completas y consistentes.</p>
            </div>
            <div className="card-pad">
              <div className="route-note">
                <strong>Resolución sugerida:</strong> 1440 px de ancho virtual, sin GPU obligatoria.
              </div>
              <div className="route-note">
                <strong>Fechas:</strong> se toma la fecha del equipo al crear cada evidencia.
              </div>
            </div>
          </section>
        </div>
      )}

      {activeTab === 'storage' && (
        <div id="settings-panel-storage" className="settings-grid" role="tabpanel" aria-labelledby="settings-tab-storage" tabIndex={0}>
          <section className="card settings-section">
            <div className="settings-section-head">
              <h3>Datos de esta instalación</h3>
              <p className="helper">Tus fichas, evidencias y reportes permanecen en este equipo.</p>
            </div>
            <div className="settings-row">
              <div>
                <strong>Fichas</strong>
                <span>Información sincronizada disponible localmente.</span>
              </div>
              <span className="status-chip ok">{fichas?.length ?? 0}</span>
            </div>
            <div className="settings-row">
              <div>
                <strong>Evidencias relacionadas</strong>
                <span>Archivos que pueden previsualizarse desde Evidencias.</span>
              </div>
              <span className="status-chip ok">{evidenceCount}</span>
            </div>
            <div className="settings-row">
              <div>
                <strong>Credenciales</strong>
                <span>No se incluyen en PDF ni respaldos.</span>
              </div>
              <span className="status-chip ok">Protegidas</span>
            </div>
            <div className="settings-row">
              <div>
                <strong>Copias recientes a conservar</strong>
                <span>La limpieza nunca toca las copias más nuevas que este número.</span>
              </div>
              <input
                className="retention-input"
                type="number"
                min={1}
                max={1000}
                value={preferences.storage.retentionKeep}
                aria-label="Copias recientes a conservar"
                onChange={(event) => setPreferences({ ...preferences, storage: { ...preferences.storage, retentionKeep: Math.min(1000, Math.max(1, Number(event.target.value) || 1)) } })}
                onBlur={() => updatePreferences(preferences)}
                disabled={saveSettings.isPending}
              />
            </div>
            <div className="settings-row">
              <div>
                <strong>Antigüedad mínima para limpiar</strong>
                <span>Solo se eliminan copias más antiguas que este número de días.</span>
              </div>
              <div className="retention-input-suffix">
                <input
                  className="retention-input"
                  type="number"
                  min={1}
                  max={3650}
                  value={preferences.storage.retentionDays}
                  aria-label="Antigüedad mínima de copias en días"
                  onChange={(event) => setPreferences({ ...preferences, storage: { ...preferences.storage, retentionDays: Math.min(3650, Math.max(1, Number(event.target.value) || 1)) } })}
                  onBlur={() => updatePreferences(preferences)}
                  disabled={saveSettings.isPending}
                />
                <span>días</span>
              </div>
            </div>
          </section>
          <section className="card settings-section">
            <div className="settings-section-head">
              <h3>Ubicación local</h3>
              <p className="helper">La carpeta se administra desde el instalador y el equipo del usuario.</p>
            </div>
            <div className="card-pad">
              <div className="route-note">Almacenamiento local de evidencias y reportes activo.</div>
            </div>
          </section>
        </div>
      )}

      {activeTab === 'backup' && (
        <div id="settings-panel-backup" className="settings-grid" role="tabpanel" aria-labelledby="settings-tab-backup" tabIndex={0}>
          <section className="card settings-section">
            <div className="settings-section-head">
              <h3>Copias de seguridad</h3>
              <p className="helper">Guarda una copia local para recuperar la base y los archivos si cambias de equipo.</p>
            </div>
            <div className="card-pad">
              <button className="button" type="button" onClick={handleBackup} disabled={createBackup.isPending}>
                {createBackup.isPending ? 'Creando copia…' : 'Crear copia ahora'}
              </button>
              <button className="button ghost" type="button" onClick={handleCleanupBackups} disabled={cleanupBackups.isPending} style={{ marginLeft: 8 }}>
                {cleanupBackups.isPending ? 'Limpiando…' : 'Limpiar antiguas'}
              </button>
              <div className="route-note" style={{ marginTop: 14 }}>
                Las credenciales de Zajuna no se incluyen en el archivo.
              </div>
              <div className="backup-list">
                <strong className="eyebrow">Copias disponibles</strong>
                {backups?.length ? backups.slice(0, 5).map((backup) => (
                  <div className="backup-row" key={backup.name}>
                    <span><b>{backup.name}</b><small>{formatBytes(backup.sizeBytes)} · {new Date(backup.createdAt).toLocaleString('es-CO')}</small></span>
                    <span className="backup-actions">
                      <a className="button ghost small" href={backupDownloadUrl(backup.name)} download aria-label={`Descargar ${backup.name}`}>Descargar</a>
                      <button className="button ghost small" type="button" onClick={() => handleRestoreBackup(backup.name)} disabled={restoreBackup.isPending}>Restaurar</button>
                      <button className="button ghost small danger-outline" type="button" onClick={() => handleDeleteBackup(backup.name)} disabled={deleteBackup.isPending}>Eliminar</button>
                    </span>
                  </div>
                )) : <p className="helper" style={{ marginTop: 10 }}>Todavía no hay copias publicadas.</p>}
              </div>
            </div>
          </section>
          <section className="card settings-section">
            <div className="settings-section-head">
              <h3>Qué incluye</h3>
            </div>
            <div className="settings-row">
              <div>
                <strong>Estado del Checklist</strong>
                <span>Los 62 puntos y sus decisiones.</span>
              </div>
              <span className="status-chip ok">Incluido</span>
            </div>
            <div className="settings-row">
              <div>
                <strong>Evidencias</strong>
                <span>Capturas y archivos manuales asociados.</span>
              </div>
              <span className="status-chip ok">Incluido</span>
            </div>
          </section>
        </div>
      )}

      {activeTab === 'notifications' && (
        <div id="settings-panel-notifications" className="settings-grid" role="tabpanel" aria-labelledby="settings-tab-notifications" tabIndex={0}>
          <section className="card settings-section">
            <div className="settings-section-head">
              <h3>Avisos de la aplicación</h3>
              <p className="helper">Los mensajes aparecen en este equipo y no dependen de correo o Slack.</p>
            </div>
            <div className="settings-row">
              <div>
                <strong>Trabajos terminados</strong>
                <span>Te avisamos cuando una sincronización o captura finalice.</span>
              </div>
              <Toggle pressed={preferences.notifications.jobCompleted} label="Avisar cuando un trabajo termine" onClick={() => togglePreference('notifications', 'jobCompleted')} />
            </div>
            <div className="settings-row">
              <div>
                <strong>Revisión necesaria</strong>
                <span>Te avisamos si una ruta necesita confirmación.</span>
              </div>
              <Toggle pressed={preferences.notifications.needsReview} label="Avisar cuando una ruta necesite revisión" onClick={() => togglePreference('notifications', 'needsReview')} />
            </div>
          </section>
        </div>
      )}

      {activeTab === 'about' && (
        <div id="settings-panel-about" className="settings-grid" role="tabpanel" aria-labelledby="settings-tab-about" tabIndex={0}>
          <section className="card settings-section">
            <div className="settings-section-head">
              <h3>Zajuna App</h3>
              <p className="helper">Herramienta local para revisar fichas y preparar evidencias.</p>
            </div>
            <div className="settings-row">
              <div>
                <strong>Procesamiento</strong>
                <span>Trabajos ejecutados en este equipo.</span>
              </div>
              <span className="status-chip ok">Local</span>
            </div>
            <div className="settings-row">
              <div>
                <strong>Interfaz</strong>
                <span>localhost y componentes visuales alineados con la maqueta.</span>
              </div>
              <span className="status-chip ok">Activa</span>
            </div>
          </section>
        </div>
      )}
    </div>
  )
}
