# Zajuna App

Aplicación de escritorio local para sincronizar Zajuna, revisar el checklist,
capturar evidencias y generar reportes.

La aplicación instalada funciona como un launcher silencioso: inicia el core
Go y abre el localhost en el navegador del sistema. Electron no crea ninguna
ventana ni WebView; solo mantiene el proceso local y evita instancias
duplicadas.

La migración completa, las decisiones técnicas, las tareas abiertas y la
matriz de pruebas están en [`docs/desktop-migration.md`](docs/desktop-migration.md).
El cierre de Linear M0/M1 (2026-08-20) está en
[`docs/hardening-2026-08-20.md`](docs/hardening-2026-08-20.md).

## Arquitectura actual

```text
Electron
  └─ core Go en 127.0.0.1:<puerto dinámico>
       ├─ React 19 + Vite embebido con go:embed
       ├─ API local same-origin
       ├─ SQLite + migraciones
       ├─ workers, scheduler y eventos
       ├─ evidencias, reportes y backups
       └─ Chromium/Playwright empaquetado
```

No requiere n8n, Docker, MySQL, ngrok, JWT ni un servidor remoto. La única
dependencia externa es la conexión HTTPS de Zajuna. La contraseña se guarda en
el almacén seguro del sistema operativo.

## Desarrollo

```powershell
npm install
npm run frontend:install
npm run browser:install
npm run desktop:dev
```

Modo de navegador local:

```powershell
npm run desktop:start
```

El launcher espera `/api/health` antes de abrir el navegador. El core continúa
escuchando únicamente en loopback y conserva su capability cookie por proceso.
Cerrar la pestaña no detiene el core; volver a ejecutar el acceso directo solo
abre de nuevo la URL de la instancia existente.

Para smoke tests sin abrir una pestaña real: `$env:ZAJUNA_SKIP_EXTERNAL_OPEN =
'1'`.

Para trabajar solo en React:

```powershell
cd frontend
npm run dev
```

## Pruebas

```powershell
npm run build --prefix frontend
npm run lint --prefix frontend
go -C core test ./...
go -C core vet ./...
npm run test:downloads
npm audit --omit=dev --audit-level=high
npm run test:browser:core
```

El smoke visual comprueba Resumen en desktop, tablet y móvil; el smoke
empaquetado inicia el ejecutable, verifica `/api/health` en loopback, el
frontend embebido, los deep links SPA, assets y respuestas 404 de API/static.

## Empaquetado

El empaquetado debe ejecutarse en el sistema objetivo para incluir el Chromium
correcto:

```powershell
npm run package:windows
npm run package:macos
npm run package:linux
```

`npm run build:platforms` genera los cores Go para Windows/Linux/macOS x64 y
ARM64. `scripts/package.cjs` exige que el runner coincida con la plataforma,
staging del core + Playwright, y genera `dist/release-manifest.json` y
`dist/sbom.cyclonedx.json`. El instalador Windows probado está en `dist/` y
actualmente no tiene firma digital.

El smoke específico del modo externo puede ejecutarse contra un paquete o,
durante desarrollo, contra Electron directamente:

```powershell
npm run test:smoke:external-browser
$env:ZAJUNA_EXTERNAL_DEV = '1'
npm run test:smoke:external-browser
```

## Carpetas principales

- `frontend/`: interfaz React y sistema visual.
- `core/`: API, SQLite, workers, capturas y reportes Go.
- `desktop/`: ciclo de vida Electron y supervisor del core.
- `scripts/`: sincronización, build, staging, metadata y smoke.
- `docs/`: arquitectura, API, auditorías, plan de migración y registro de hardening.
