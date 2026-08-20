const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const { writeMetadata } = require('./release-metadata.cjs');
const { writeDownloadsPage } = require('./prepare-downloads.cjs');

const projectRoot = path.resolve(__dirname, '..');
const rawArgs = process.argv.slice(2);

function argumentValue(name) {
  const index = rawArgs.indexOf(name);
  return index >= 0 ? rawArgs[index + 1] : undefined;
}

function requestedPlatform() {
  if (rawArgs.includes('--win')) return 'win';
  if (rawArgs.includes('--mac')) return 'mac';
  if (rawArgs.includes('--linux')) return 'linux';
  if (process.platform === 'win32') return 'win';
  if (process.platform === 'darwin') return 'mac';
  return 'linux';
}

function requestedArch() {
  if (rawArgs.includes('--arm64')) return 'arm64';
  if (rawArgs.includes('--ia32')) return 'ia32';
  return 'x64';
}

const platform = requestedPlatform();
const arch = requestedArch();
const targetId = {
  win: { x64: 'windows-x64', arm64: 'windows-arm64' },
  linux: { x64: 'linux-x64', arm64: 'linux-arm64' },
  mac: { x64: 'macos-x64', arm64: 'macos-arm64' },
}[platform]?.[arch];

if (!targetId) {
  console.error(`No existe un core preparado para ${platform}/${arch}. Usa x64 o arm64 y ejecuta npm run core:build:targets.`);
  process.exit(1);
}

const hostPlatform = process.platform === 'win32' ? 'win' : process.platform === 'darwin' ? 'mac' : 'linux';
if (platform !== hostPlatform) {
  console.error(`El empaquetado de ${platform} debe ejecutarse en un runner ${platform}; el host actual es ${hostPlatform}. Esto evita incluir Chromium de otra plataforma.`);
  process.exit(1);
}

const binary = platform === 'win' ? 'zajuna-core.exe' : 'zajuna-core';
const sourceDir = path.join(projectRoot, 'dist', 'core-targets', targetId);
const sourceBinary = path.join(sourceDir, binary);
if (!fs.existsSync(sourceBinary)) {
  console.error(`Falta ${path.relative(projectRoot, sourceBinary)}. El empaquetado no usará el core del host: ejecuta npm run core:build:targets.`);
  process.exit(1);
}

const stagingDir = path.join(projectRoot, 'tmp', 'package-staging', targetId, 'core');
fs.rmSync(path.dirname(stagingDir), { recursive: true, force: true });
fs.mkdirSync(stagingDir, { recursive: true });
const stagedBinary = path.join(stagingDir, binary);
fs.copyFileSync(sourceBinary, stagedBinary);
if (platform !== 'win') fs.chmodSync(stagedBinary, 0o755);

const playwrightSource = path.join(projectRoot, 'core', 'bin', 'playwright');
if (!fs.existsSync(playwrightSource)) {
  console.error(`Falta ${path.relative(projectRoot, playwrightSource)}. Ejecuta npm run browser:install en el runner ${platform} antes de empaquetar.`);
  process.exit(1);
}
fs.cpSync(playwrightSource, path.join(stagingDir, 'playwright'), { recursive: true });

const configPath = path.join(projectRoot, 'tmp', `electron-builder.${targetId}.json`);
const config = {
  appId: 'com.zajuna.app',
  productName: 'Zajuna App',
  files: ['desktop/**/*', 'package.json'],
  extraResources: [{ from: path.relative(projectRoot, stagingDir).replaceAll(path.sep, '/'), to: 'core' }],
  directories: { output: 'dist' },
  win: { target: 'nsis' },
  mac: { target: 'dmg' },
  linux: { target: 'AppImage' },
};
fs.writeFileSync(configPath, `${JSON.stringify(config, null, 2)}\n`);

const cliPath = require.resolve('electron-builder/cli.js');
const filteredArgs = [];
for (let index = 0; index < rawArgs.length; index += 1) {
  if (rawArgs[index] === '--config') {
    index += 1;
    continue;
  }
  filteredArgs.push(rawArgs[index]);
}
if (!filteredArgs.some((arg) => arg === '--' || arg.startsWith('--win') || arg.startsWith('--mac') || arg.startsWith('--linux'))) {
  filteredArgs.push(`--${platform}`);
}
filteredArgs.push('--config', configPath);

console.log(`Empaquetando ${platform}/${arch} con ${path.relative(projectRoot, sourceBinary)}.`);
const result = spawnSync(process.execPath, [cliPath, ...filteredArgs], { cwd: projectRoot, stdio: 'inherit' });
if (result.error) {
  console.error(`No se pudo ejecutar electron-builder: ${result.error.message}`);
  process.exit(1);
}
if (result.status !== 0) process.exit(result.status ?? 1);

writeMetadata();
writeDownloadsPage();
