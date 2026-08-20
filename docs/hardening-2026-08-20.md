# Hardening 2026-08-20 — Zajuna App

Registro de la jornada sobre el proyecto Linear
[Zajuna-App](https://linear.app/medialab-sena/project/zajuna-app-6d193f12dc3d).
La issue madre es [MDL-25](https://linear.app/medialab-sena/issue/MDL-25).

No se afirma un gate de release. Las pruebas ejecutadas en esta estación
Windows (Node v24.17.0, npm 11.13.0) fueron:

```text
go -C core test ./...
go -C core vet ./...
npm ci --prefix frontend
node node_modules/typescript/bin/tsc -b
node node_modules/vite/bin/vite.js build
node node_modules/oxlint/bin/oxlint
node scripts/prepare-downloads.test.cjs
```

Todas terminaron con exit 0. No se ejecutó el E2E autenticado contra Zajuna
real, ni firma/notarización, ni smoke nativo de macOS/Linux, ni WCAG con
lector de pantalla.

## Issues cerradas en código (In Review)

| Issue | Milestone | Qué quedó en el repo |
|---|---|---|
| [MDL-26](https://linear.app/medialab-sena/issue/MDL-26) | M0 | `BrowserCookieForTarget` y `ValidateCaptureNavigationURL`. Cookies con domain/path/Secure/HttpOnly/SameSite/Expires. Se rechazan orígenes ajenos, loopback y esquemas no permitidos antes de capturar. |
| [MDL-27](https://linear.app/medialab-sena/issue/MDL-27) | M0 | Vite usa PostCSS inline. El build no lee `C:\postcss.config.mjs`. Fast Refresh en 0 warnings. |
| [MDL-28](https://linear.app/medialab-sena/issue/MDL-28) | M0 | `scripts/prepare-downloads.cjs` genera la guía de descargas. El artefacto sin firma es un bloqueo de release, no un paso para eludir SmartScreen/Gatekeeper. |
| [MDL-30](https://linear.app/medialab-sena/issue/MDL-30) | M1 | Transiciones CAS de jobs, un worker por id, recuperación de `running`/`retrying`/`queued` al arrancar. |
| [MDL-31](https://linear.app/medialab-sena/issue/MDL-31) | M1 | Backup con SHA256 y schema. `integrity_check` en staging. Swap atómico y rollback si `sqlite.Open` falla. |
| [MDL-33](https://linear.app/medialab-sena/issue/MDL-33) | M1 | CAPTCHA/MFA abortan (`zajuna_challenge_required`). Sesión vencida no captura en anónimo. Falta E2E vivo. |

## Issues que siguen abiertas (Todo)

| Issue | Milestone | Bloqueo |
|---|---|---|
| [MDL-29](https://linear.app/medialab-sena/issue/MDL-29) | M2 | Certificados Authenticode/Developer ID y runners nativos Windows/macOS/Linux. |
| [MDL-32](https://linear.app/medialab-sena/issue/MDL-32) | M2 | Pasada manual con teclado, zoom 200 %, NVDA y VoiceOver. |
| [MDL-34](https://linear.app/medialab-sena/issue/MDL-34) | M2 | Gate de release: repetir la matriz completa y acta. Depende de MDL-29/32 y del E2E vivo. |

## Cómo retomar

1. Cuenta de prueba: `$env:ZAJUNA_E2E='1'` más `ZAJUNA_TEST_USERNAME` y
   `ZAJUNA_TEST_PASSWORD` (solo entorno; nunca en git) y
   `go -C core test ./cmd/zajuna-core -run TestAuthenticatedZajunaE2E`.
2. Firma: secretos `CSC_LINK` / Apple ID en CI, nunca en issues ni logs.
3. WCAG: recorrer las nueve rutas con teclado y lector, registrar hallazgos
   como issues hijas de MDL-32.
4. Gate: MDL-34 solo se marca Done con logs/artefactos frescos.
