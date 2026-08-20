# Migración de Zajuna Sync a Zajuna App Desktop

**Estado del documento:** consolidado después de la auditoría OWASP y del
hardening M0/M1 (2026-08-20).  
**Fecha:** 2026-08-20  
**Código de entrega:** repositorio `Zajuna-App`

El registro de Linear de esa jornada está en
[`hardening-2026-08-20.md`](hardening-2026-08-20.md). Este documento sigue
siendo el punto de partida para pruebas de cliente y decisiones de release.
El repositorio `D:\zajuna\zajuna-sync-` solo se consulta como referencia; no se
ejecuta, modifica ni se distribuye con la aplicación.

## 1. Objetivo y límites

Zajuna App es una aplicación de escritorio instalable para Windows, macOS y
Linux. Todo el procesamiento principal ocurre localmente:

- Electron funciona como launcher silencioso y supervisor del ciclo de vida.
- Un core Go escucha únicamente en loopback y sirve API + frontend.
- React 19 + TypeScript + Vite es la interfaz distribuida.
- SQLite guarda datos de negocio, jobs, evidencias, reportes y avisos.
- Chromium/Playwright se distribuye con el instalador para capturas y PDF.
- Credential Manager, Keychain o Secret Service guardan la contraseña de Zajuna.

No forman parte del runtime final n8n, Docker, MySQL, ngrok, JWT, un login
propio, un servidor remoto ni una cuenta de Zajuna App. Zajuna sigue siendo la
dependencia HTTPS externa necesaria para sincronizar y capturar información.

## 2. Qué se migró

### 2.1 Interfaz

La interfaz vanilla fue sustituida por React y se conserva el sistema visual de
la maqueta offline: tokens de color, tipografías locales, estados semánticos,
SVG inline, animaciones y responsive.

Rutas funcionales actuales:

| Ruta | Responsabilidad |
|---|---|
| `/resumen` | Métricas, ficha activa, atención, job en curso, programación y actividad reciente. |
| `/fichas` | Tabla de fichas, selección y sincronización. |
| `/checklist` | 15 categorías, 62 ítems, estados `SI/NO/PENDIENTE`, slots y actividades. |
| `/checklist/:itemCode` | Detalle, evidencias, targets e historial auditable del ítem. |
| `/actividades` | Selección local de actividades técnicas. |
| `/evidencias` | Galería plana y agrupada, filtros, miniaturas, carga, selección y borrado confirmado. |
| `/trabajos` | Historial, filtros, progreso y estados terminales. |
| `/trabajos/:jobId` | Timeline, eventos, cancelación y diagnóstico de un job. |
| `/reportes` | Generación y descarga de HTML/PDF local. |
| `/configuracion` | Cuenta, capturas, almacenamiento, copias y notificaciones locales. |
| `/diagnostico` | Salud del core, SQLite, Chromium, almacenamiento y últimos fallos. |
| `/notificaciones` | Centro local de avisos de jobs. |

Se corrigieron además los estados de carga/error/vacío, filtros, ARIA, foco,
menú móvil, confirmación de borrado y contraste de la interfaz.

### 2.2 Core Go y persistencia

El core contiene:

- Setup inicial y conexión técnica con Zajuna.
- Cliente HTTP con cookies efímeras y redacción de tokens.
- Tras sincronizar, extrae el nombre visible del perfil autenticado y lo guarda
  como metadata local no sensible para mostrar iniciales; la contraseña y las
  cookies nunca se persisten.
- Workers de sync, descubrimiento, captura HTML/PNG, checklist y reportes.
- Runtime de jobs con polling, eventos, progreso, cancelación y reintentos.
- Scheduler local y programación persistente.
- Checklist de 62 ítems y 15 categorías.
- Historial de cambios por ítem y decisiones de rutas.
- Galería, hashes, metadata, reportes y backups ZIP locales.
- Diagnóstico y centro de notificaciones en SQLite schema v12.

La API expone, entre otros, `/api/setup`, `/api/fichas`, `/api/checklist`,
`/api/course-maps`, `/api/jobs`, `/api/schedules`, `/api/evidences`,
`/api/reports`, `/api/backups`, `/api/settings`, `/api/diagnostics` y
`/api/notifications`. El contrato completo está en
[`api-local.md`](api-local.md).

### 2.3 Desktop y distribución

Electron inicia el core con puerto dinámico, espera `/api/health`, abre el
frontend embebido en el navegador del sistema y cierra el proceso al salir. Si
el core muere, el supervisor intenta recuperarlo hasta tres veces, conserva
logs redacted rotativos y vuelve a abrir el endpoint.

### Modos de presentación

El modo de entrega abre en el navegador del sistema la URL loopback servida por
Go. Electron funciona únicamente como launcher/supervisor sin `BrowserWindow`,
inicia el core con la misma supervisión y vuelve a abrir el endpoint si el core
se recupera. El modo no expone el servidor fuera de loopback ni elimina la
capability cookie. Cerrar la pestaña no detiene el core.

El bloqueo de instancia única hace que un segundo acceso directo reutilice el
backend existente y abra el endpoint actual en el navegador, sin matar ni
duplicar procesos.

El pipeline actual es:

```text
Vite build
  → sync a core/cmd/zajuna-core/web
  → cross-build de cores Go
  → instalar Chromium en el runner nativo
  → staging core + playwright por target
  → electron-builder
  → smoke /api/health + manifest SHA256 + SBOM CycloneDX
```

Targets Go generados actualmente: Windows x64/ARM64, Linux x64/ARM64 y macOS
x64/ARM64. Los instaladores DMG/AppImage deben construirse y probarse en sus
respectivos runners nativos porque Chromium y la firma son específicos de cada
sistema.

## 3. Bloques realizados

1. **Inventario y límites:** se separó la lógica rescatable de la infraestructura
   n8n/Angular/vanilla y se fijó el destino único `Zajuna.App`.
2. **Base local:** Go + SQLite + Electron + Playwright reemplazaron servicios
   remotos y MySQL.
3. **Datos y dominio:** migraciones versionadas, fichas, cursos, checklist,
   evidencias, reportes, schedules, backups y notificaciones.
4. **Workers:** sincronización, mapas, captura autenticada, HTML/PNG/PDF y
   progreso persistido.
5. **React:** cliente API tipado, normalización PascalCase/camelCase, React
   Query, polling, rutas reales y CSS/design system de la maqueta.
6. **Paridad de diseño:** tarjetas, filtros, galería, timeline, motion,
   skeletons, estados de error y responsive.
7. **Empaquetado:** frontend embebido con `go:embed`, fallback SPA, staging por
   plataforma, supervisor Electron y smoke del instalador.
8. **OWASP:** capability cookie por proceso, validación loopback/Origin,
   límites HTTP, allowlist anti-SSRF, redacción de secretos y protección de
   symlinks.
9. **Calidad de interfaz:** menú móvil accesible, focus trap, confirmaciones,
   ARIA, contraste, invalidación de queries y filtros completos.

## 4. Seguridad y riesgos abiertos

### Cerrado en este bloque

- Las mutaciones locales requieren capability cookie `HttpOnly` y
  `SameSite=Strict` emitida por el proceso.
- Se valida `Host`, `Origin`, `Sec-Fetch-Site`, `Content-Type`, tamaño y
  timeouts HTTP.
- Las capturas de producción limitan origen, bloquean IPs privadas y validan
  redirects/final URL.
- URLs, metadata, errores y mapas redaccionan `sesskey`, tokens, cookies y
  credenciales.
- Rutas de evidencias rechazan symlinks que salgan de la carpeta autorizada.
- El launcher no crea ventanas ni expone APIs Node al navegador del usuario;
  la interfaz corre en el navegador predeterminado contra loopback.
- El archivo temporal del endpoint usa un nonce por ejecución y el launcher
  rechaza URLs con origen, credenciales, query o fragmento distintos de
  `http://127.0.0.1:<puerto>/`.

### Cerrado en código (2026-08-20)

- Jobs: transiciones CAS, un worker por id y recuperación de huérfanos al arrancar.
- Backups: SHA256, `PRAGMA integrity_check`, schema y rollback si `sqlite.Open` falla.
- Captura: cookies acotadas al origen, URL final validada y aborto de CAPTCHA/MFA.

### Pendiente antes de una entrega comercial

| Prioridad | Tarea | Motivo |
|---|---|---|
| P0 | Firma digital de instaladores y ejecutables (MDL-29). | Evitar advertencias de Windows/macOS y asegurar procedencia. |
| P0 | Smoke nativo de DMG/AppImage y ciclo instalar/actualizar/desinstalar (MDL-29). | La estación actual solo valida Windows. |
| P1 | Pasada manual WCAG con teclado, NVDA y VoiceOver (MDL-32). | El smoke automatizado no sustituye un lector de pantalla. |
| P1 | E2E autenticado con cuenta de prueba real (MDL-33). | Fixtures cubren login vencido, CAPTCHA y redirects; falta cuenta viva. |
| P1 | Gate de release con matriz y acta (MDL-34). | No afirmar versión lista sin logs/artefactos frescos. |
| P2 | Completar workflows administrativos y adaptadores externos opcionales. | No bloquean el runtime local principal. |

La auditoría manual OWASP no encontró vulnerabilidades npm de producción; el
escáner automatizado Codex Security no pudo iniciar por un fallo de su
workbench, por lo que no se presenta una certificación automática.

CAPTCHA y MFA no se resuelven automáticamente. Si Zajuna muestra reCAPTCHA,
hCaptcha o un segundo factor, el cliente HTTP y Chromium abortan con
`zajuna_challenge_required` y no capturan. Una sesión vencida produce
`zajuna_session_expired` y no guarda evidencia anónima. La prueba viva se
ejecuta con `ZAJUNA_E2E=1` y credenciales solo en variables de entorno
(`ZAJUNA_TEST_USERNAME`, `ZAJUNA_TEST_PASSWORD`); nunca se versionan.

## 5. Pruebas y criterios de aceptación

Pruebas verdes en Windows durante esta consolidación:

```text
npm ci --prefix frontend
npm run build --prefix frontend
npm run lint --prefix frontend       # oxlint exit 0
go -C core test ./...
go -C core vet ./...
node scripts/prepare-downloads.test.cjs
npm audit --omit=dev --audit-level=high
```

El smoke visual se ejecuta en 1440×900, 1024×900 y 390×844, verifica overflow,
nombres accesibles, menú móvil y hashes estables después de neutralizar la
rasterización variable del texto del `<select>` nativo. La última ejecución
pasó dos veces consecutivas.

El instalador Windows actual incluye core Go y Chromium/Playwright, responde a
`/api/health` y mide aproximadamente 346 MB. Sigue **sin firma digital**.

## 6. Cómo continuar

El detalle de Linear está en [`hardening-2026-08-20.md`](hardening-2026-08-20.md).

1. Ejecutar el E2E autenticado con cuenta de prueba (`ZAJUNA_E2E=1`) y registrar
   selectores no sensibles (MDL-33).
2. Firmar instaladores y correr smoke nativo en Windows, macOS y Linux (MDL-29).
3. Pasada manual WCAG con teclado, zoom 200 % y NVDA/VoiceOver (MDL-32).
4. Gate de release con matriz y acta; no marcar Done sin artefactos (MDL-34).
5. Solo después preparar logo, iconos, actualización automática y publicación.
