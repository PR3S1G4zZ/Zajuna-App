# Arquitectura de Zajuna App Desktop

La arquitectura vigente está descrita en detalle en
[`desktop-migration.md`](desktop-migration.md). Este documento resume los
límites entre procesos, datos y seguridad.

## Componentes

### Electron

`desktop/main.cjs` es un launcher silencioso sin `BrowserWindow`. Solicita una
instancia única, inicia el core con `--port=0` y un archivo temporal de
endpoint, espera `/api/health`, abre la URL loopback con el navegador del
sistema y termina el proceso durante `before-quit`. Un supervisor detecta la
muerte del core, guarda stderr redacted en logs rotativos de 1 MiB e intenta
recuperarlo hasta tres veces. Un segundo lanzamiento reutiliza el endpoint y
abre otra pestaña, sin crear otro backend.

El launcher no confía ciegamente en el archivo temporal: cada endpoint usa un
nonce criptográfico por ejecución y se acepta únicamente `http://127.0.0.1` con
puerto válido, sin credenciales, query ni fragmento, antes de hacer health-check
o abrir el navegador. Esto evita que un archivo temporal manipulado redirija el
launcher a un origen remoto.

### Core Go

El binario en `core/cmd/zajuna-core` sirve los assets React embebidos mediante
`//go:embed` y la API `/api/*` desde el mismo origen. Escucha en loopback,
selecciona un puerto libre y conserva fallback SPA para rutas profundas de
React. No contiene login propio, usuarios locales ni JWT.

El middleware local aplica:

1. `Host` loopback y coincidencia de `Origin`/`Sec-Fetch-Site`.
2. Capability cookie aleatoria por proceso (`HttpOnly`, `SameSite=Strict`) para
   mutaciones.
3. `Content-Type` esperado y límite de cuerpo.
4. Timeouts de lectura/escritura/idle y `MaxHeaderBytes`.
5. Headers de seguridad y respuestas de error sin secretos.

### Frontend

`frontend/` es React 19 + TypeScript + Vite. `frontend/dist` se sincroniza en
`core/cmd/zajuna-core/web` antes del build Go. React Query gestiona polling,
invalidación y estados de carga/error; React Router maneja las rutas de las
vistas. Las fuentes y assets se empaquetan localmente para funcionamiento
offline de la interfaz.

### Persistencia

SQLite vive en la carpeta de datos del usuario. El schema actual es v12 e
incluye fichas, cursos, mapas, targets, jobs, eventos, schedules, evidencias,
reportes, settings, backups, historial del checklist y notificaciones.

La contraseña se escribe exclusivamente en Credential Manager (Windows),
Keychain (macOS) o Secret Service (Linux). Cookies y tokens permanecen en
memoria; URLs y eventos pasan por redacción antes de persistirse.

Los backups ZIP incluyen snapshot SQLite, hashes SHA256 y `schemaVersion`. El
restore valida `PRAGMA integrity_check` y el schema en staging; si `Open`
falla tras el swap, se restaura la base anterior.

### Workers y Chromium

Los workers registrados incluyen sync de fichas, conexión, descubrimiento de
mapas, captura HTML/browser/checklist y exportación de reportes. El runtime de
jobs persiste estado, eventos, reintentos y progreso. Las transiciones son
CAS en SQLite: un worker por job y, al arrancar, los `running` huérfanos
pasan a `retrying` (o `failed` si no quedan intentos). Playwright Go usa el
runtime instalado bajo `core/bin/playwright`; el empaquetado copia esa carpeta
junto al core en `resources/core/playwright`.

Las capturas de producción permiten únicamente el origen Zajuna configurado,
bloquean IPs privadas/loopback, comprueban cookies de sesión contra ese
origen, validan la URL final antes del screenshot y redaccionan metadata.
CAPTCHA/MFA abortan la captura. Los helpers de pruebas pueden usar servidores
locales controlados.

## Flujo de ejecución

```text
Electron
  → inicia core Go y espera endpoint
  → abre navegador predeterminado en loopback
  → React llama API same-origin con capability cookie
  → API crea job persistente
  → worker usa SQLite, keyring y Chromium
  → eventos/polling actualizan React
  → resultado local: evidencia, reporte, backup o diagnóstico
```

## Build y distribución

```text
frontend build → sync web → Go targets → Chromium del runner nativo
→ staging target → electron-builder → smoke → manifest/SBOM
```

El staging se valida contra el host: no se permite generar un instalador macOS
o Linux desde Windows con Chromium incorrecto. Los cores cross-compiled sí se
generan para x64 y ARM64, pero el instalador debe probarse nativamente.

## Riesgos aún abiertos

- Firma digital y notarización (MDL-29).
- Smoke nativo de DMG/AppImage y ciclo instalar/actualizar/desinstalar (MDL-29).
- Prueba manual WCAG con lector de pantalla (MDL-32).
- E2E autenticado con cuenta de prueba real (MDL-33).
- Gate de release con matriz y acta (MDL-34).
