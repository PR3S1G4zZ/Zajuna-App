const fs = require('node:fs');
const path = require('node:path');

const projectRoot = path.resolve(__dirname, '..');
const distRoot = path.join(projectRoot, 'dist');
const outputPath = path.join(distRoot, 'downloads.html');

const BYPASS_PHRASES = [
  'Ejecutar de todas formas',
  'Run anyway',
  'Open Anyway',
  'Open anyway',
  'Más información y luego',
  'Disable Gatekeeper',
  'xattr -c',
  'chmod +x y ejecutar igual',
];

function loadManifest() {
  const manifestPath = path.join(distRoot, 'release-manifest.json');
  if (!fs.existsSync(manifestPath)) {
    const packageJson = JSON.parse(fs.readFileSync(path.join(projectRoot, 'package.json'), 'utf8'));
    return {
      name: packageJson.name,
      productName: packageJson.build?.productName ?? packageJson.name,
      version: packageJson.version,
      signed: false,
      artifacts: [],
    };
  }
  return JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
}

function escapeHTML(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

function artifactRows(artifacts) {
  if (!artifacts.length) {
    return '<p>Todavía no hay artefactos publicados. Cuando existan, esta página listará el archivo, el tamaño y el SHA256 del manifiesto oficial.</p>';
  }
  const rows = artifacts.map((artifact) => `
      <tr>
        <td><code>${escapeHTML(artifact.file)}</code></td>
        <td>${escapeHTML(artifact.size ?? '')}</td>
        <td><code>${escapeHTML(artifact.sha256)}</code></td>
      </tr>`).join('');
  return `
    <table>
      <thead>
        <tr><th>Archivo</th><th>Tamaño</th><th>SHA256</th></tr>
      </thead>
      <tbody>${rows}
      </tbody>
    </table>`;
}

function renderDownloadsPage(manifest = loadManifest()) {
  const signed = manifest.signed === true;
  const status = signed
    ? '<p class="ok">Este canal publica instaladores firmados. Comprueba el editor/firma del sistema operativo y el checksum del manifiesto antes de instalar.</p>'
    : `<section class="blocker">
        <h2>Release bloqueado: artefacto no firmado</h2>
        <p>El instalador actual no tiene firma digital. Eso es un bloqueo de publicación, no un paso de instalación.</p>
        <p>No ignores las advertencias de Windows, Gatekeeper de macOS ni del escritorio Linux. Espera el canal oficial firmado o verifica el SHA256 del manifiesto solo si tu equipo de entrega te pidió validar un candidato interno.</p>
      </section>`;

  return `<!DOCTYPE html>
<html lang="es">
<head>
  <meta charset="utf-8">
  <title>Descargas de ${escapeHTML(manifest.productName || manifest.name || 'Zajuna App')}</title>
</head>
<body>
  <h1>Descargas de ${escapeHTML(manifest.productName || 'Zajuna App')}</h1>
  <p>Versión ${escapeHTML(manifest.version || 'sin publicar')}. Descarga únicamente desde el canal oficial y contrasta el archivo con el manifiesto SHA256.</p>
  ${status}
  <h2>Cómo instalar de forma segura</h2>
  <ol>
    <li>Descarga el instalador desde el canal oficial del equipo de entrega.</li>
    <li>Abre el manifiesto y comprueba que el SHA256 del archivo coincide exactamente.</li>
    <li>En Windows, verifica el editor/Authenticode. En macOS, verifica Developer ID y notarización. En Linux, usa el AppImage junto al checksum publicado.</li>
    <li>Si el sistema operativo muestra una advertencia de archivo no reconocido, detente. No eludas SmartScreen ni Gatekeeper.</li>
  </ol>
  <h2>Artefactos</h2>
  ${artifactRows(manifest.artifacts || [])}
</body>
</html>
`;
}

function assertSafeCopy(html) {
  for (const phrase of BYPASS_PHRASES) {
    if (html.toLowerCase().includes(phrase.toLowerCase())) {
      throw new Error(`la guía de descargas no puede contener instrucciones de bypass: ${phrase}`);
    }
  }
}

function writeDownloadsPage() {
  const html = renderDownloadsPage();
  assertSafeCopy(html);
  fs.mkdirSync(distRoot, { recursive: true });
  fs.writeFileSync(outputPath, html);
  console.log(`Página de descargas escrita en ${path.relative(projectRoot, outputPath)}`);
  return outputPath;
}

if (require.main === module) writeDownloadsPage();

module.exports = { BYPASS_PHRASES, renderDownloadsPage, writeDownloadsPage, outputPath };
