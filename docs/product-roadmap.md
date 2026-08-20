# Roadmap vigente de Zajuna App

El documento completo de migración está en [`desktop-migration.md`](desktop-migration.md).
El cierre de la jornada 2026-08-20 está en [`hardening-2026-08-20.md`](hardening-2026-08-20.md).
Este roadmap solo muestra el estado de trabajo y las tareas que faltan.

## Estado actual

| Área | Estado | Evidencia |
|---|---|---|
| Launcher Electron + core Go | Implementado | Launcher silencioso, endpoint dinámico, instancia única, recuperación y navegador predeterminado. |
| React embebido | Implementado | Vite → `go:embed`, PostCSS local, fallback SPA y API same-origin. |
| SQLite y secretos | Implementado | Schema v12, keyring, backups con hash/`integrity_check` y rollback si el restore no abre. |
| Jobs y scheduler | Implementado | CAS, un worker por job, recuperación de huérfanos al arrancar, eventos y schedules. |
| Checklist/evidencias/reportes | Implementado | 62 ítems, detalle, galería, capturas y PDF/HTML. |
| Configuración/diagnóstico/notificaciones | Implementado | APIs locales y vistas funcionales. |
| Fidelidad visual y accesibilidad automatizada | Implementado | Sistema de diseño, motion, responsive y smoke de tres viewports. |
| Seguridad OWASP | Hardening principal implementado | Capability, Host/Origin, anti-SSRF, cookies de captura acotadas, redacción y symlink guard. |
| Instalador Windows | Construido y probado | NSIS x64 con core + Chromium; sin firma digital. Guía de descargas sin bypass de SmartScreen. |
| macOS/Linux | Cross-build preparado | Falta ejecutar instalador y smoke en runners nativos. |

## Fases cerradas

### Fase 1 — Alcance e inventario

Se definió `Zajuna.App` como único destino y `zajuna-sync` como referencia no
modificable. Cada workflow se convirtió en un caso de uso local con trigger,
input, steps, progreso, resultado y error.

### Fase 2 — Runtime local

Se reemplazaron n8n, webhooks, MySQL, Docker y túneles por Go, SQLite,
workers, scheduler y Chromium empaquetado. La contraseña se trasladó al
almacén seguro del sistema operativo.

### Fase 3 — Dominio y persistencia

Se implementaron fichas, cursos, mapas de captura, checklist, slots,
evidencias, reportes, backups, settings, diagnóstico y notificaciones. El
schema actual es v12.

### Fase 4 — React y maqueta

La interfaz vanilla se migró a React 19 + TypeScript + Vite. Se portaron
tipografías, tokens, estados, animaciones, galerías, timeline, filtros y rutas
reales. Se añadieron menú móvil, foco, ARIA, confirmaciones y estados de error.

### Fase 5 — Entrega

El build sincroniza React dentro del core, genera seis targets Go, instala
Chromium en el runner nativo, hace staging por plataforma y produce metadata
SHA256/SBOM. El smoke empaquetado verifica `/api/health`.

### Fase 6 — Hardening M0/M1 (2026-08-20)

Se cerraron en código los bloqueadores de build/pruebas, las transiciones de
jobs, el restore seguro de SQLite y el aborto de CAPTCHA/MFA. Queda M2:
firma nativa, WCAG manual y gate de release. Ver Linear MDL-25.

## Trabajo restante priorizado

### P0 — Antes de entregar una versión comercial

1. Firmar el instalador y ejecutables con certificados del cliente (MDL-29).
2. Crear y probar DMG macOS y AppImage Linux en máquinas nativas (MDL-29).
3. Probar instalación limpia, actualización, desinstalación y ausencia de
   procesos huérfanos (MDL-29).
4. Ejecutar flujo autenticado con cuenta de prueba en Windows (MDL-33, E2E vivo).

### P1 — Antes de beta amplia

1. Ejecutar pruebas manuales WCAG: teclado, zoom 200 %, NVDA y VoiceOver (MDL-32).
2. Registrar selectores y reglas por curso real en el E2E autenticado (MDL-33).
3. Repetir revisión OWASP y el gate de integración (MDL-34).

### P2 — Evolución posterior

1. Workflows administrativos restantes y adaptadores de correo/Slack/IMAP.
2. Logo, iconos, firma/notarización automatizada y actualización automática.
3. Optimización del tamaño de Chromium y pruebas de carga de capturas paralelas.

## Criterio de finalización

La versión estará lista cuando pueda instalarse en los tres sistemas, configure
credenciales sin exponerlas, sincronice una ficha, capture evidencia, genere
PDF, recupere/cancele jobs, restaure un backup válido, cierre limpiamente y
pase las pruebas unitarias, integración, browser, visuales, WCAG y OWASP.
