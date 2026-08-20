const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { BYPASS_PHRASES, renderDownloadsPage } = require('./prepare-downloads.cjs');

function testForbidsBypassLanguage() {
  const html = renderDownloadsPage({
    name: 'zajuna-app',
    productName: 'Zajuna App',
    version: '0.1.0',
    signed: false,
    artifacts: [{ file: 'Zajuna App Setup 0.1.0.exe', size: 346000000, sha256: 'abc123' }],
  });
  for (const phrase of BYPASS_PHRASES) {
    assert.equal(html.toLowerCase().includes(phrase.toLowerCase()), false, `found bypass phrase: ${phrase}`);
  }
  assert.equal(html.includes('Ejecutar de todas formas'), false);
  assert.equal(html.includes('Más información y luego'), false);
}

function testExplainsUnsignedAsReleaseBlocker() {
  const html = renderDownloadsPage({
    productName: 'Zajuna App',
    version: '0.1.0',
    signed: false,
    artifacts: [{ file: 'Zajuna-App.AppImage', sha256: 'def456' }],
  });
  assert.match(html, /Release bloqueado: artefacto no firmado/);
  assert.match(html, /bloqueo de publicación/);
  assert.match(html, /SHA256/);
  assert.match(html, /canal oficial/);
  assert.doesNotMatch(html, /Ejecutar de todas formas/);
}

function testKeepsChecksumAndOfficialChannelWhenSigned() {
  const html = renderDownloadsPage({
    productName: 'Zajuna App',
    version: '0.1.0',
    signed: true,
    artifacts: [{ file: 'Zajuna-App.dmg', size: 320000000, sha256: 'fff999' }],
  });
  assert.match(html, /fff999/);
  assert.match(html, /Zajuna-App\.dmg/);
  assert.match(html, /canal oficial/);
  assert.match(html, /Authenticode|Developer ID|checksum/i);
  fs.mkdtempSync(path.join(os.tmpdir(), 'zajuna-downloads-'));
}

testForbidsBypassLanguage();
testExplainsUnsignedAsReleaseBlocker();
testKeepsChecksumAndOfficialChannelWhenSigned();
console.log('prepare-downloads.test.cjs: 3 passed');
