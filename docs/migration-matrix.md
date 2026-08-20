# Matriz de migración y tareas

Esta matriz parte del estado real de `Zajuna.App` después de la migración a
desktop. `zajuna-sync` solo aporta workflows, fixtures y reglas de negocio; no
es una dependencia de ejecución.

## Estados

- **Completado:** implementado y cubierto por pruebas o smoke.
- **Hardening:** funcional, pero requiere una corrección antes de release.
- **Validación:** código presente; falta probar contra Zajuna real o en otro OS.
- **P2:** opcional y no bloquea el runtime local.

## Matriz principal

| Capacidad | Implementación local | Estado | Próxima acción |
|---|---|---|---|
| Setup inicial | `POST /api/setup`, config no sensible + keyring, perfil visible tras sync | Completado | Probar reconfiguración/eliminación y nombre del perfil en cliente. |
| Secretos y sesión | `core/internal/secrets`, cliente HTTP y Chromium | Completado | CAPTCHA/MFA abortan; falta E2E vivo (MDL-33). |
| Sincronizar fichas | `SyncFichasWorker` + SQLite | Validación | Ejecutar con cuenta de prueba y varios cursos. |
| Checklist 62 ítems | Catálogo local, 15 categorías, estados y slots | Completado | Validar reglas por curso. |
| Detalle de ítem | `/checklist/:itemCode`, historial y evidencias | Completado | Revisar casos sin targets o con errores. |
| Actividades técnicas | Selección local por ficha | Completado | Confirmar selección con un curso real. |
| Mapas de curso | `DiscoverCourseMapsWorker`, BFS y proyección por slot | Validación | Probar cambios de navegación de Zajuna. |
| Captura HTML | `CaptureEvidenceWorker`, hash y metadata | Completado | Ejecutar lote real. |
| Captura PNG/PDF | Browser/checklist/report workers + Chromium | Validación | Probar descargas, WAF, CAPTCHA y MFA. |
| Evidencia manual | Upload, galería, selección y borrado confirmado | Completado | Revisar permisos y archivos grandes. |
| Jobs y progreso | Runtime persistente, CAS, eventos, polling y detalle | Completado | Repetir recuperación real tras un cierre forzado. |
| Cancelación/reintentos | Context cancellation, CAS y backoff | Completado | Cancelar un job terminal se rechaza; un worker por id. |
| Scheduler | Schedules locales y tarjeta de Resumen | Completado | Probar cierre de UI con job programado. |
| Reportes | HTML/PDF local con evidencias agrupadas | Completado | Validar fuentes y PDF en macOS/Linux. |
| Backups | ZIP, hash, `integrity_check`, schema y rollback | Completado | Probar restore en una instalación de cliente. |
| Configuración | Cuenta, capturas, almacenamiento, copias y avisos | Completado | Validar preferencias en instalación limpia. |
| Diagnóstico | Core, SQLite, Chromium, disco y jobs | Completado | Exportar log redacted y probar fallos reales. |
| Notificaciones | Tabla v12, preferencias y centro local | Completado | Validar lectura masiva y cierre/reapertura. |
| Frontend React | 12 rutas, React Query, router, CSS y fuentes offline | Completado | Añadir runner unitario cuando el producto lo requiera. |
| Fidelidad mockup | Estados, motion, responsive, SVG y galería | Completado | Revisión manual de las 16 pantallas. |
| Accesibilidad | ARIA, foco, menú móvil, contraste y smoke | Hardening | NVDA, VoiceOver, teclado completo y zoom 200 % (MDL-32). |
| API local | Go loopback, SPA fallback y contratos documentados | Hardening | Probar capability/Origin con cliente externo. |
| Anti-SSRF | Allowlist Zajuna, IP privada y redirect guard | Completado | Repetir pruebas con DNS/redirects reales. |
| Redacción de secretos | URLs, errores, eventos y metadata | Completado | Añadir revisión de logs de instalación. |
| Launcher local | Supervisor, recovery, instancia única, logs rotativos y navegador predeterminado sin BrowserWindow | Completado | Probar doble lanzamiento, cierre forzado y core ausente en cada OS. |
| Core Windows | NSIS x64 con core + Playwright | Completado | Entregar instalador a QA Windows. |
| Core ARM64/cross-build | Windows/Linux/macOS x64 y ARM64 | Completado | Smoke nativo de cada instalador. |
| Firma y publicación | Manifest SHA256, SBOM y guía de descargas | Hardening | Firma, notarización y canal oficial (MDL-29, MDL-28). |

## Infraestructura descartada

| Elemento heredado | Decisión |
|---|---|
| n8n, webhooks y callbacks | Sustituidos por workers Go, eventos y scheduler local. |
| JWT, registro y login propio | Eliminados; solo existe setup local y sesión técnica contra Zajuna. |
| MySQL | Sustituido por SQLite y migraciones locales. |
| Docker, sidecar Puppeteer y ngrok | Sustituidos por Chromium/Playwright local y loopback. |
| Google Sheets/Drive obligatorios | Sustituidos por SQLite y backups ZIP locales. |
| Slack/correo obligatorios | Sustituidos por notificaciones locales; adaptadores P2. |

## Definition of Done por bloque

### Funcionalidad

- Setup, sync, checklist, captura, reporte, backup y diagnóstico funcionan sin
  servicios auxiliares.
- Cada job deja estado, eventos, resultado y diagnóstico.
- La UI muestra loading, error, vacío y estado terminal.

### Seguridad

- Mutaciones sin capability, Host/Origin válido o Content-Type correcto son
  rechazadas.
- Capturas no llegan a loopback/IP privada ni siguen redirects fuera de
  allowlist.
- Contraseñas, cookies y tokens no aparecen en SQLite, logs, eventos o API.

### Distribución

- El instalador lleva el core y Chromium del mismo target.
- El smoke abre la aplicación, comprueba `/api/health` y no deja procesos.
- El artefacto tiene manifest, checksums y SBOM; firma pendiente hasta disponer
  de certificado.
