const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');

const projectRoot = path.resolve(__dirname, '..');
const distRoot = path.join(projectRoot, 'dist');
const packageJson = JSON.parse(fs.readFileSync(path.join(projectRoot, 'package.json'), 'utf8'));
const lockPath = path.join(projectRoot, 'package-lock.json');

function sha256(filePath) {
  return crypto.createHash('sha256').update(fs.readFileSync(filePath)).digest('hex');
}

function productionComponents() {
  if (!fs.existsSync(lockPath)) return [];
  const lock = JSON.parse(fs.readFileSync(lockPath, 'utf8'));
  return Object.entries(lock.packages ?? {})
    .filter(([location, pkg]) => location && location !== '' && pkg && pkg.version)
    .map(([location, pkg]) => ({
      type: 'library',
      name: pkg.name ?? location.replace(/^node_modules[\\/]/, ''),
      version: pkg.version,
      purl: `pkg:npm/${encodeURIComponent(pkg.name ?? location)}@${pkg.version}`,
      properties: [{ name: 'zajuna:dependency-scope', value: pkg.dev === true ? 'build' : 'runtime' }],
    }))
    .sort((a, b) => `${a.name}@${a.version}`.localeCompare(`${b.name}@${b.version}`));
}

function writeMetadata() {
  if (!fs.existsSync(distRoot)) {
    throw new Error('No existe dist/. Ejecuta un empaquetado antes de generar metadata.');
  }

  const artifacts = fs.readdirSync(distRoot, { withFileTypes: true })
    .filter((entry) => entry.isFile())
    .filter((entry) => !['release-manifest.json', 'sbom.cyclonedx.json'].includes(entry.name))
    .map((entry) => {
      const filePath = path.join(distRoot, entry.name);
      return {
        file: entry.name,
        size: fs.statSync(filePath).size,
        sha256: sha256(filePath),
      };
    })
    .sort((a, b) => a.file.localeCompare(b.file));

  const manifest = {
    name: packageJson.name,
    productName: packageJson.build?.productName ?? packageJson.name,
    version: packageJson.version,
    node: process.version,
    artifacts,
  };
  fs.writeFileSync(path.join(distRoot, 'release-manifest.json'), `${JSON.stringify(manifest, null, 2)}\n`);

  const sbom = {
    bomFormat: 'CycloneDX',
    specVersion: '1.5',
    serialNumber: `urn:zajuna:${packageJson.name}:${packageJson.version}`,
    version: 1,
    metadata: {
      component: { type: 'application', name: packageJson.name, version: packageJson.version },
    },
    components: productionComponents(),
  };
  fs.writeFileSync(path.join(distRoot, 'sbom.cyclonedx.json'), `${JSON.stringify(sbom, null, 2)}\n`);
  console.log(`Metadata de release generada para ${artifacts.length} artefacto(s).`);
}

if (require.main === module) {
  writeMetadata();
  require('./prepare-downloads.cjs').writeDownloadsPage();
}
module.exports = { writeMetadata };
