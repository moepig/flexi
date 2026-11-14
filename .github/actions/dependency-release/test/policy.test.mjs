import test from 'node:test';
import assert from 'node:assert/strict';
import { automaticUpdate, metadata, releaseImpact, squash, validateChecksums, validateFile } from '../src/policy.mjs';

const config = { files: ['package.json', 'package-lock.json', 'go.mod', 'go.sum', 'app.csproj', 'Dockerfile', 'global.json'], actions: ['actions/checkout'], images: ['golang'] };
const patch = { name: 'a', type: 'version-update:semver-patch' };
const major = { name: 'b', type: 'version-update:semver-major' };

test('minor/patch can merge; grouped major and unknown updates require review', () => {
  assert.equal(automaticUpdate([patch]), true);
  assert.equal(automaticUpdate([patch, major]), false);
  assert.equal(automaticUpdate([{ name: 'a', type: 'unknown' }]), false);
  assert.equal(automaticUpdate([patch, major], ['b']), true);
});
test('signed metadata is parsed and missing/duplicate metadata fails closed', () => {
  assert.deepEqual(metadata('Bump a\n\n---\nupdated-dependencies:\n- dependency-name: a\n  update-type: version-update:semver-patch\n...\n'), [patch]);
  assert.throws(() => metadata('Update a'));
  assert.throws(() => metadata('---\nupdated-dependencies: []\nupdated-dependencies: []\n...\n'));
});
test('only npm dependency fields may change', () => {
  const a = { scripts: { test: 'vitest' }, dependencies: { a: '1.0.0' } };
  const b = { ...a, dependencies: { a: '1.0.1' } };
  assert.deepEqual(validateFile('package.json', JSON.stringify(a), JSON.stringify(b), config).names, ['a']);
  assert.throws(() => validateFile('package.json', JSON.stringify(a), JSON.stringify({ ...b, scripts: { postinstall: 'curl attacker' } }), config));
  assert.throws(() => validateFile('run.sh', 'true', 'false', config));
  assert.throws(() => validateFile('package.json', null, '{}', config));
});
test('workflows permit full-SHA updates of existing allowlisted actions', () => {
  const old = 'name: CI\non: push\njobs:\n  test:\n    steps:\n      - uses: actions/checkout@' + 'a'.repeat(40) + '\n';
  const next = old.replace('a'.repeat(40), 'b'.repeat(40));
  validateFile('.github/workflows/ci.yml', old, next, config);
  assert.throws(() => validateFile('.github/workflows/ci.yml', old, next + '    env: { MALICIOUS: true }\n', config));
  assert.throws(() => validateFile('.github/workflows/ci.yml', old, next.replace('actions/checkout', 'evil/action'), config));
  assert.throws(() => validateFile('.github/workflows/ci.yml', old, next.replace('b'.repeat(40), 'v7'), config));
});
test('Go version changes are allowed while replace directives are held', () => {
  const before = 'module example.com/test\n\ngo 1.27.2\n\nrequire example.com/a v1.0.0\n';
  validateFile('go.mod', before, before.replace('1.27.2', '1.27.3').replace('v1.0.0', 'v1.0.1'), config);
  assert.throws(() => validateFile('go.mod', before, before + 'replace example.com/a => ../evil\n', config));
});
test('NuGet permits version attributes and rejects executable MSBuild changes', () => {
  const before = '<Project><PackageReference Include="A" Version="1.0.0" /></Project>';
  validateFile('app.csproj', before, before.replace('1.0.0', '1.0.1'), config);
  assert.throws(() => validateFile('app.csproj', before, before.replace('</Project>', '<Target Name="Build"><Exec Command="evil" /></Target></Project>'), config));
});
test('Docker only permits existing image references and .NET SDK only its version', () => {
  validateFile('Dockerfile', 'FROM golang:1.27.2\nRUN true', 'FROM golang:1.27.3\nRUN true', config);
  assert.throws(() => validateFile('Dockerfile', 'FROM golang:1.27.2\nRUN true', 'FROM golang:1.27.3\nRUN evil', config));
  validateFile('global.json', '{"sdk":{"version":"10.0.100"}}', '{"sdk":{"version":"10.0.101"}}', config);
});
test('unknown product impact releases; test-only groups do not; mixed groups release', () => {
  const exclusions = [{ name: 'a', files: ['go.mod', 'go.sum'], reason: 'Imported by tests only' }];
  const changes = [{ kind: 'go', manifest: 'go.mod' }, { kind: 'go', manifest: 'go.sum' }];
  assert.equal(releaseImpact([patch], changes, exclusions), false);
  assert.equal(releaseImpact([patch, major], changes, exclusions), true);
  assert.equal(releaseImpact([patch], changes, []), true);
  assert.equal(releaseImpact([patch], [], exclusions), true);
});
test('product-impact updates produce releasable squash messages and optional breaking notes', () => {
  assert.match(squash([patch], true, 1, 'a'.repeat(40)).title, /^fix\(deps\):/);
  assert.match(squash([patch], false, 1, 'a'.repeat(40)).title, /^chore\(deps\):/);
  assert.match(squash([patch], true, 1, 'a'.repeat(40), 'configuration changed').body, /BREAKING CHANGE: configuration changed/);
});
test('checksums require every asset and reject duplicates and paths', () => {
  const hash = 'a'.repeat(64);
  assert.equal(validateChecksums(`${hash}  app.zip`, ['app.zip']).get('app.zip'), hash);
  assert.throws(() => validateChecksums(`${hash}  other.zip`, ['app.zip']));
  assert.throws(() => validateChecksums(`${hash}  app.zip\n${hash}  app.zip`, ['app.zip']));
  assert.throws(() => validateChecksums(`${hash}  ../app.zip`, ['app.zip']));
});

test('a test dependency cannot hide a change shared with a product build tool', () => {
  const cfg = { ...config, noRelease: [{ name: 'vitest', files: ['package.json'], reason: 'Test runner' }] };
  const lock = { name: 'app', version: '0.1.0', lockfileVersion: 3, packages: {
    '': { dependencies: { react: '19' }, devDependencies: { vitest: '4', vite: '7' } },
    'node_modules/react': { version: '19.0.0' },
    'node_modules/vite': { version: '7.0.0', dev: true },
    'node_modules/vitest': { version: '4.0.0', dependencies: { vite: '7' }, dev: true },
  } };
  const next = structuredClone(lock);
  next.packages['node_modules/vite'].version = '7.0.1';
  const change = validateFile('package-lock.json', JSON.stringify(lock), JSON.stringify(next), cfg);
  assert.equal(change.runtime, true);
  assert.equal(releaseImpact([{ name: 'vitest', type: patch.type }], [change], cfg.noRelease), true);
  next.packages['node_modules/vite'].version = '7.0.0';
  next.packages['node_modules/vitest'].version = '4.0.1';
  assert.equal(validateFile('package-lock.json', JSON.stringify(lock), JSON.stringify(next), cfg).runtime, false);
});
