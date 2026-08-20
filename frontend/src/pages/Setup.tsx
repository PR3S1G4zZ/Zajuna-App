import { useState, type FormEvent } from 'react'
import { useSaveSetup } from '../hooks/api'
import { useToast } from '../hooks/useToast'
import { friendlyError } from '../lib/friendlyError'

export function Setup() {
  const saveSetup = useSaveSetup()
  const toast = useToast()
  const [documentType, setDocumentType] = useState('CC')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')

  async function handleSubmit(event: FormEvent) {
    event.preventDefault()
    setError('')
    try {
      await saveSetup.mutateAsync({ zajunaUsername: username.trim(), zajunaDocumentType: documentType, zajunaPassword: password })
      toast('Cuenta conectada correctamente.')
    } catch (err) {
      const message = err instanceof Error ? err.message : 'No se pudo guardar la configuración.'
      setError(message)
      toast(message, true)
    }
  }

  return (
    <>
      <header className="topbar">
        <div className="brand">
          <span className="brand-mark">Z</span>
          <span>
            Zajuna App
            <span className="brand-subtitle">Operación local</span>
          </span>
        </div>
      </header>
      <main className="shell setup-shell">
        <div className="setup-layout">
        <aside className="setup-summary card" aria-label="Resumen del proyecto">
          <div className="card-pad">
            <div className="eyebrow">Zajuna App</div>
            <h2 style={{ marginTop: 8 }}>Tu espacio de trabajo local</h2>
            <p className="helper" style={{ marginTop: 8 }}>
              Guarda tus credenciales una sola vez y completa el flujo desde este equipo, sin exponer tus evidencias fuera de él.
            </p>
            <ol className="setup-steps">
              <li className="active"><b>1</b><span><strong>Conectar cuenta</strong><small>Protegemos la contraseña en este equipo.</small></span></li>
              <li><b>2</b><span><strong>Sincronizar fichas</strong><small>Traemos tus cursos y programas.</small></span></li>
              <li><b>3</b><span><strong>Buscar rutas</strong><small>Localizamos las secciones que necesitas.</small></span></li>
              <li><b>4</b><span><strong>Capturar y reportar</strong><small>Revisas evidencias y generas tu reporte.</small></span></li>
            </ol>
          </div>
        </aside>
        <section className="setup card">
          <div className="card-pad">
            <div className="eyebrow">Primer paso</div>
            <h1>Conecta tu cuenta de Zajuna</h1>
            <p className="muted">
              Usaremos tus datos para consultar tus fichas y preparar las evidencias. Se guardan de forma segura en este
              equipo.
            </p>
            <div className="setup-note">
              <i>i</i>
              <span>Si Zajuna solicita una verificación adicional, te avisaremos y podrás resolverla desde la ventana del navegador.</span>
            </div>
            {error && (
              <p className="helper" style={{ color: 'var(--no)', marginTop: 14 }}>
                {friendlyError(error)}
              </p>
            )}
            <form onSubmit={handleSubmit}>
              <div className="field">
                <label htmlFor="document-type">Tipo de documento</label>
                <select id="document-type" value={documentType} onChange={(event) => setDocumentType(event.target.value)}>
                  <option value="CC">Cédula de ciudadanía (CC)</option>
                  <option value="TI">Tarjeta de identidad (TI)</option>
                  <option value="CE">Cédula de extranjería (CE)</option>
                </select>
              </div>
              <div className="field">
                <label htmlFor="username">Número de documento</label>
                <input
                  id="username"
                  inputMode="numeric"
                  autoComplete="username"
                  required
                  value={username}
                  onChange={(event) => setUsername(event.target.value)}
                />
              </div>
              <div className="field">
                <label htmlFor="password">Contraseña de Zajuna</label>
                <input
                  id="password"
                  type="password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                />
              </div>
              <div className="form-actions">
                <button className="button primary" type="submit" disabled={saveSetup.isPending}>
                  {saveSetup.isPending ? 'Guardando…' : 'Continuar'}
                </button>
              </div>
            </form>
          </div>
        </section>
        </div>
        <p className="helper" style={{ textAlign: 'center' }}>
          Tus fichas, evidencias y reportes se procesan en este equipo.
        </p>
      </main>
    </>
  )
}
