# API local de Zajuna App

La API se sirve desde el core Go en el mismo origen que la interfaz y escucha
exclusivamente en `127.0.0.1`. No usa JWT, login propio ni CORS abierto para la
interfaz integrada.

## Protección del origen local

Al iniciar el core se genera una capability aleatoria por proceso. Las
respuestas emiten la cookie `zajuna_capability` con `HttpOnly` y
`SameSite=Strict`; el navegador integrado la reenvía automáticamente en las
mutaciones. `POST`, `PUT`, `PATCH` y `DELETE` requieren además:

- `Host` loopback y `Origin` coincidente cuando el navegador lo envía.
- `Sec-Fetch-Site` que no sea `cross-site`.
- `Content-Type` JSON para cuerpos JSON o `multipart/form-data` para uploads.
- Límites de tamaño, headers y timeouts del servidor.

Un cliente externo no debe copiar la cookie ni asumir que el endpoint es un
servidor público. Los helpers de tests que crean `newRouterWithServices`
prueban handlers sin middleware para aislar cada caso; el runtime real de
`main` siempre registra `protectLocalAPI`.

Las rutas de captura solo aceptan el origen Zajuna configurado en producción,
rechazan loopback/IP privadas y validan redirects y URL final. Los errores,
eventos y metadata pasan por redacción de tokens, cookies y parámetros
sensibles antes de responder o persistir.

## Salud y configuración

### `GET /api/health`

Devuelve el estado del core, versión y plataforma.

### `GET /api/setup/status`

Devuelve si la configuración inicial está completa y el usuario configurado.
Nunca devuelve la contraseña.

### `POST /api/setup`

```json
{
  "zajunaUsername": "123456789",
  "zajunaDocumentType": "CC",
  "zajunaPassword": "••••••••"
}
```

La contraseña se guarda en el almacén seguro del sistema operativo. La
configuración no sensible se guarda localmente.

### `POST /api/zajuna/test-connection`

Encola una prueba real de autenticación contra Zajuna y validación de `Mis
cursos`. No devuelve ni persiste cookies o contraseñas; el resultado se
consulta como cualquier otro job.

La sesión HTTP envía `documentType` (por defecto `CC`) y tolera las dos
variantes actuales del formulario de Zajuna: `logintoken` solo, o
`logintoken` acompañado de `josso`. Ningún token o cookie se devuelve por la
API ni se persiste localmente.

```json
{
  "username": "123456789",
  "documentType": "CC"
}
```

### `POST /api/course-maps/discover`

Encola el descubrimiento local de rutas para los cursos sincronizados. Si no
se envía `courseIds`, el core usa los cursos guardados en SQLite. El worker
reutiliza una sesión HTTP efímera, recorre enlaces internos con límites,
lee también opciones del selector “Ir a…” y conserva títulos de actividad.
Después construye pools ordenados para fases, cronogramas, foros, anuncios,
asignaciones, sesiones y perfil; esos pools se proyectan a `itemCode` y slot.

```json
{
  "username": "123456789",
  "documentType": "CC",
  "courseIds": ["41080"],
  "maxDepth": 2,
  "maxPages": 80
}
```

Los límites son opcionales. La respuesta es `202 Accepted`; el resultado y
los eventos se consultan mediante `/api/jobs/{id}` y
`/api/jobs/{id}/events`.

### `POST /api/course-maps/import-activities`

Importa el resultado seguro del script de DevTools cuando se necesita validar
manualmente un curso. Solo acepta `courseId`, `profileUrl` opcional y arreglos
de enlaces con `path`/`label`; rechaza campos desconocidos y nunca recibe
cookies, contraseñas ni tokens.

```json
{
  "courseId": "41080",
  "profileUrl": "https://zajuna.sena.edu.co/zajuna/user/profile.php",
  "pageLinks": [{ "path": "/zajuna/mod/page/view.php?id=10", "label": "Cronograma General" }],
  "jump": [{ "path": "/zajuna/mod/forum/view.php?id=20", "label": "Foro de Dudas e Inquietudes" }]
}
```

La respuesta es `201 Created` y devuelve el mapa persistido. El importador
conserva el orden de los enlaces para que la asignación de slots coincida con
el orden mostrado por Zajuna.

Para diagnosticar manualmente un curso autenticado existe
`scripts/dev/export-zajuna-course-links.js`. Se puede pegar en la consola de
desarrollador de Chrome y copia únicamente URLs y títulos de enlaces; no lee
cookies, contraseñas ni tokens. Es una herramienta de diagnóstico, no una
dependencia del runtime: la aplicación repite esta extracción desde su worker
local.

### `GET /api/course-maps?limit=50`

Lista los mapas persistidos localmente con sus rutas normalizadas, estadísticas
y advertencias de límites.

### `GET /api/course-maps/{courseId}`

Devuelve el mapa completo de un curso o `404` si todavía no fue descubierto.

## Fichas y checklist

### `GET /api/fichas?limit=100`

Lista las fichas sincronizadas localmente. Cada ficha incluye su identificador
local, código externo, curso y última sincronización.

### `GET /api/checklist/dashboard?fichaId=<id>`

Devuelve la ficha activa, el resumen de progreso, las 15 categorías y sus 62
ítems. Si no se envía `fichaId`, usa la ficha activa persistida en SQLite.

La interfaz presenta cada categoría con su título y cantidad de ítems. También
tolera respuestas con nombres de campos JSON en mayúsculas o minúsculas, para
que el menú de secciones siga siendo legible aunque cambie el serializador.

```json
{
  "activeFichaId": "ficha-…",
  "summary": { "total": 62, "yes": 0, "no": 0, "pending": 62, "percentage": 0 },
  "items": [
    {
      "itemCode": "1.1.1",
      "status": "PENDIENTE",
      "maxEvidences": 1,
      "evidenceCount": 0
    }
  ]
}
```

### `POST /api/fichas/active`

Selecciona la ficha de trabajo y devuelve su dashboard.

```json
{ "fichaId": "ficha-…" }
```

### `PATCH /api/checklist/items/{itemCode}/status`

Actualiza de forma persistente el estado de un ítem. Los valores aceptados
son `SI`, `NO` y `PENDIENTE`.

```json
{ "fichaId": "ficha-…", "status": "SI" }
```

La respuesta devuelve el dashboard recalculado para que la interfaz actualice
el progreso general y el de la categoría sin mantener un estado paralelo.

### `GET /api/checklist/items/{itemCode}?fichaId=<id>`

Devuelve el ítem solicitado con sus slots/evidencias y hasta 50 eventos de
historial ordenados del más reciente al más antiguo. Cada cambio manual guarda
estado anterior, estado nuevo, origen y fecha en SQLite; no incluye cookies ni
credenciales.

### `GET /api/checklist/activities?fichaId=<id>`

Devuelve las actividades `assign` normalizadas desde el mapa local del curso.
Cada actividad incluye título, URL, fase, sección, indicador técnico y si fue
seleccionada por el instructor. La selección es local por ficha y excluye las
vistas de calificación o navegación.

Si la ficha existe pero todavía no tiene un mapa de rutas, la respuesta sigue
siendo `200` y devuelve `mapReady:false`, una lista vacía y `discovery` con la
acción `discover-course-maps`. Esto representa un estado normal de primer uso,
no un error de consulta; la interfaz debe ofrecer el CTA **Buscar rutas**.
Una ficha inexistente conserva la respuesta `404`.

`GET /api/checklist/targets?fichaId=<id>` usa el mismo protocolo de primer uso:
cuando la ficha no tiene mapa responde `200` con `mapReady:false`, objetivos
vacíos y `discovery.action:discover-course-maps`; cuando el mapa está listo
incluye `mapReady:true`.

### `PUT /api/checklist/activities`

Reemplaza de forma atómica las actividades que el instructor declara como
propias. El servidor valida que cada ID pertenezca al mapa actual del curso.
Si aún no existe el mapa, responde `409` con `code:course_map_required` y la
acción `discover-course-maps`, sin crear un trabajo fallido.

```json
{
  "fichaId": "ficha-…",
  "selectedActivityIds": ["3010294", "3010361"]
}
```

Debe ejecutarse antes de capturar las evidencias ligadas a fechas y entregas.

### `GET /api/checklist/targets?fichaId=<id>`

Resuelve las rutas del mapa local de la ficha en objetivos dirigidos por
`itemCode`. La respuesta incluye el resumen de ítems/slots resueltos, la
selección configurada y los selectores o etiquetas que aplicará Chromium. Para
`6.1`, `10.1.1` y `10.1.2`, una selección activa hace que el objetivo use el
menú principal del curso, abra la sección correspondiente y capture el bloque
`#module-<activityId>` con sus fechas.

### `GET /api/checklist/reviews?fichaId=<id>`

Lista las decisiones locales para las rutas agrupadas. Los estados son
`review`, `confirmed` y `correction`; una revisión puede incluir un enlace o
selector manual sin exponer credenciales ni cookies.

### `PUT /api/checklist/reviews`

Guarda o reemplaza una revisión. La ruta debe pertenecer al mapa actual de la
ficha y un enlace manual debe mantener el mismo origen de Zajuna.

```json
{
  "fichaId": "ficha-…",
  "routeKey": "cronograma_general|https://zajuna.sena.edu.co/zajuna/mod/page/view.php?id=10|page",
  "status": "confirmed",
  "manualUrl": "",
  "manualSelector": "",
  "note": ""
}
```

Las revisiones se guardan en SQLite y `capture-checklist` las aplica en su
próxima ejecución.

### `POST /api/checklist/capture`

Encola una captura autenticada de los objetivos disponibles para la ficha. Si
no se envían `username` y `documentType`, se toman de la configuración local.
`itemCodes` permite limitar la ejecución a tareas concretas y `maxTargets`
protege ejecuciones de prueba.

```json
{
  "fichaId": "ficha-…",
  "itemCodes": ["1.1.1", "1.2.1"],
  "maxTargets": 20
}
```

Cada PNG queda en la carpeta local de evidencias, se registra con `source`
`capture-checklist`, slot, hash, URL final, actividad, fase, selector y si el
selector coincidió. Los selectores semánticos son estrictos: si no existe el
bloque requerido, el objetivo falla y no se guarda una página genérica como si
fuera la evidencia correcta. Las capturas repetidas actualizan el mismo slot
de checklist en lugar de crear duplicados.

Los objetivos de perfil usan la página completa. En foros y anuncios, la
captura de configuración usa el contenedor completo sin la tabla de respuestas;
los objetivos de contenido exigen un post o fila asociado al instructor
autenticado. La captura no continúa con un fallback genérico si esa identidad
no está disponible. Para cronogramas se prioriza el contenido de Google Sheets
embebido y, si el curso usa HTML, se captura el bloque HTML con viewport ancho.
La estructura esperada es la del cronograma de referencia: hoja general y
secciones por fase con nombre de fase, actividades, resultados, evidencias,
área y fechas de inicio/finalización. La vista general no se etiqueta como
exclusiva del área técnica; las relaciones técnicas se determinan por las
actividades seleccionadas por el instructor y por el área/competencia
detectada.

### Captura PNG autenticada

La captura se encola mediante `POST /api/jobs` con `type` igual a
`capture-browser`. Para una ruta de Zajuna puede solicitarse una sesión técnica
efímera:

```json
{
  "type": "capture-browser",
  "input": {
    "url": "https://zajuna.sena.edu.co/zajuna/course/view.php?id=41080",
    "authenticated": true,
    "username": "123456789",
    "documentType": "CC"
  }
}
```

El worker valida el mismo origen, carga las cookies solo en el contexto
Chromium de ese job y persiste la evidencia PNG con hash. No guarda cookies ni
contraseñas. Si Zajuna devuelve la pantalla de login, el job termina con
`zajuna_session_expired` para que la UI indique una acción de recuperación.

## Jobs

### `GET /api/jobs?limit=20`

Lista los últimos jobs persistidos, ordenados por actividad. `limit` acepta un
valor entre 1 y 100.

### `POST /api/jobs`

Crea un job y devuelve `202 Accepted`.

```json
{
  "type": "sync-fichas",
  "input": {
    "username": "123456789",
    "documentType": "CC"
  }
}
```

Respuesta resumida:

```json
{
  "id": "job-…",
  "type": "sync-fichas",
  "status": "queued",
  "progress": 0,
  "stage": "",
  "attempt": 0,
  "maxAttempts": 3
}
```

### `GET /api/jobs/{id}`

Consulta el estado de un job. Los estados posibles son:

`queued`, `running`, `waiting_user`, `retrying`, `completed`, `failed`,
`cancelled`.

### `GET /api/jobs/{id}/events`

Devuelve la secuencia de eventos de progreso y transición del job. Los eventos
no contienen contraseñas, cookies ni tokens.

### `POST /api/jobs/{id}/cancel`

Solicita la cancelación del job. El worker recibe la señal mediante
`context.Context` y debe cerrar sus recursos antes de terminar.

## Automatizaciones locales

### `GET /api/schedules`

Lista las tareas programadas persistidas en SQLite. Un schedule contiene el
tipo de worker, su input JSON, intervalo en segundos, estado habilitado y la
próxima ejecución.

### `POST /api/schedules`

```json
{
  "workerType": "sync-fichas",
  "input": { "username": "123456789", "documentType": "CC" },
  "intervalSeconds": 86400,
  "enabled": true
}
```

El scheduler local entrega el input al runtime de workers y conserva la
cadencia aunque la aplicación haya estado cerrada. No depende de n8n ni de un
servicio remoto.

### `POST /api/schedules/{id}/enabled`

```json
{ "enabled": false }
```

Habilita o pausa un schedule sin eliminar su configuración.

## Preferencias y diagnóstico

### `GET /api/settings` / `PUT /api/settings`

Lee o reemplaza preferencias no sensibles de sesión, captura y avisos. El
servidor valida la forma del documento y lo guarda en `app_settings`; las
contraseñas siguen exclusivamente en el almacén seguro del sistema. El bloque
`storage` contiene `retentionKeep` (1–1000 copias) y `retentionDays` (1–3650
días), que alimentan la limpieza de backups desde Configuración.

### `GET /api/diagnostics`

Ejecuta comprobaciones locales de core, SQLite, presencia de credencial,
Chromium, carpeta de datos y trabajos fallidos. Devuelve estados `ok`, `warn`
o `error` e incidencias identificadas por job/error code. No realiza la prueba
remota de Zajuna durante el polling y no devuelve mensajes sensibles.

### `GET /api/notifications`

Lista hasta 50 avisos locales ordenados del más reciente al más antiguo. Los
avisos de jobs solo incluyen el tipo de resultado, código seguro y referencia
al job; no se copia el mensaje de error que pudiera contener contenido externo.

### `POST /api/notifications/{id}/read` y `POST /api/notifications/read-all`

Marca un aviso o todos los avisos como leídos. La preferencia de avisos en
`/api/settings` controla si se generan avisos de trabajos completados o de
trabajos que requieren atención.

## Backups

### `POST /api/backups`

Crea una copia ZIP en la carpeta local de backups. Incluye un snapshot
consistente de SQLite, configuración no sensible y artefactos locales de
evidencia/reportes si existen. Las contraseñas y secretos del almacén del
sistema nunca se incluyen.

### `GET /api/backups` y `GET /api/backups/{name}/download`

Lista copias publicadas y permite descargar una copia validada por nombre. La
API nunca expone la ruta absoluta del equipo.

### `DELETE /api/backups/{name}`

Elimina una copia local después de la confirmación explícita en la interfaz.

### `POST /api/backups/cleanup`

Aplica la política conservadora de retención local. Por defecto conserva las
cinco copias más recientes y solo elimina archivos con más de 30 días. Puede
recibir `{ "keep": 7, "olderThanDays": 45 }`; la UI toma ambos valores del
bloque `storage` de `/api/settings` y solicita confirmación antes de ejecutar.

### `POST /api/backups/{name}/restore`

Valida el ZIP (archivos regulares, SHA256 del snapshot y versión de schema) y
lo deja preparado en una carpeta privada para el siguiente arranque. Antes
crea automáticamente una copia de seguridad de protección y responde `202`
con `restartRequired: true`; la base activa nunca se reemplaza mientras el
core está atendiendo peticiones.

En el arranque, `StageRestore` ejecuta `PRAGMA integrity_check` y comprueba
las tablas mínimas (`schema_migrations`, `jobs`, `fichas`, `evidences`)
antes de marcar `.restore-pending`. El swap es atómico. Si `sqlite.Open`
falla después del swap, el core restaura `*.restore-old`, registra
`.restore-applied.json` y reintenta abrir la base anterior. Un ZIP corrupto,
con hash incorrecto o schema fuera de 1…12 se rechaza y no toca la DB activa.

## Evidencias y reportes

### `GET /api/evidences?limit=50&fichaId=<id>`

Lista evidencias locales con formato, origen, fecha, SHA-256 y metadatos de
ficha/ítem. `fichaId` limita la consulta a una ficha y alimenta la galería de
miniaturas seleccionables; la agrupación para reportes se conserva aparte.

### `GET /api/evidences/{id}/download`

Sirve el archivo de evidencia únicamente si permanece dentro de la carpeta
local de evidencias.

El dashboard incluye los enlaces de descarga de las evidencias asociadas a
cada ítem del checklist. La galería visual también permite abrir una vista
previa de cada grupo sin salir de la aplicación.

### `POST /api/evidences/upload`

Recibe un formulario `multipart/form-data` con `file`, `fichaId` y, de forma
opcional, `itemCode`. Acepta PNG, JPG, PDF y HTML hasta 25 MB. El archivo se
guarda dentro del almacenamiento local, se calcula su SHA-256 y se registra
con origen `manual`.

### `DELETE /api/evidences/{id}`

Elimina con una operación explícita el archivo y su registro local. La API
solo permite eliminar artefactos que estén dentro de la carpeta local de
evidencias.

### `GET /api/evidences/groups?fichaId=<id>`

Lista las representaciones agrupadas por URL, selector y grupo funcional. Cada
grupo conserva los `itemCode` y slots que cubre, mientras el reporte utiliza la
captura más reciente como representación visual.

La galería local permite buscar por título, código de actividad o descripción,
y filtrar por nivel de confianza (`sugerida`, `confirmada` o `manual`) antes de
abrir la vista previa de un grupo. Cuando hay más de seis grupos, la interfaz
ofrece mostrar la lista completa sin alterar la agrupación utilizada por el
reporte.

La revisión guiada de rutas también admite búsqueda por sección/actividad y
filtros de estado (`por revisar`, `confirmadas` y `para corregir`).

### `POST /api/evidences/groups/rebuild`

Reconstruye los grupos de una ficha. El proceso mantiene los archivos y filas
originales, pero no publica en grupos ni reportes una captura automática que
termine en la página pública/login de Zajuna, que use un selector legado de
perfil/foro o que sea un fixture fuera del catálogo del checklist.

### `POST /api/reports`

Encola la generación de un reporte mediante `export-report`.

```json
{
  "title": "Reporte mensual",
  "format": "pdf",
  "evidenceLimit": 100
}
```

`format` puede ser `pdf` o `html`. El PDF se renderiza con el Chromium
empaquetado; el HTML se genera directamente en el core.

### `GET /api/reports?limit=20`

Lista reportes locales terminados.

### `GET /api/reports/{id}/download`

Descarga o abre el archivo local de un reporte validado.

La interfaz muestra los últimos reportes en el panel “Reportes disponibles”,
con su estado y un botón “Abrir PDF” cuando la generación terminó. El usuario
puede generar otro PDF desde ese mismo panel; el proceso se encola y el estado
se actualiza automáticamente.

## Contratos de workers disponibles

| Tipo | Estado |
|---|---|
| `sync-fichas` | Implementado; sesión HTTP de Zajuna y persistencia SQLite. |
| `test-zajuna-connection` | Implementado; login real y validación de `Mis cursos` sin escritura de fichas. |
| `discover-course-maps` | Implementado; crawl HTTP autenticado, fases/actividades, rutas clasificadas y persistencia SQLite. |
| `capture-evidence` | Implementado; descarga HTML local con hash. |
| `capture-browser` | Implementado; captura PNG local con Chromium empaquetado y sesión Zajuna efímera opcional. |
| `capture-checklist` | Implementado; resuelve `itemCode`/slots desde el mapa local, captura regiones con Chromium y registra evidencias por tarea. |
| `export-report` | Implementado; genera HTML/PDF local. |

El cliente debe mostrar el progreso que devuelve la API y no ejecutar trabajos
largos dentro de la petición HTTP.

## Fichas

### `GET /api/fichas?limit=50`

Lista las fichas sincronizadas localmente. La respuesta incluye identificador
externo, nombre, curso, estado y fecha de sincronización.

### `POST /api/fichas/sync`

Encola `sync-fichas` para la interfaz y el scheduler. Si no se envía usuario,
usa el usuario guardado en la configuración local.

```json
{
  "username": "123456789",
  "documentType": "CC"
}
```
