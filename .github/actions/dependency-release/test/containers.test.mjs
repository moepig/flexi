import test from 'node:test';
import assert from 'node:assert/strict';
import { inspectImage } from '../src/containers.mjs';

const sha = 'a'.repeat(40);
function registry({ missing = false, forbidden = false, wrongSha = false } = {}) {
  return { token: 'test', async fetcher(url) {
    if (url.includes('/token?')) return { ok: true, json: async () => ({ token: 'registry-test' }) };
    if (forbidden) return { ok: false, status: 403 };
    if (missing) return { ok: false, status: 404 };
    if (url.endsWith('/manifests/1.0.0')) return { ok: true, headers: new Map([['docker-content-digest', 'sha256:' + 'b'.repeat(64)]]), json: async () => ({ manifests: ['amd64', 'arm64'].map(architecture => ({ digest: `sha256:${architecture}`, platform: { os: 'linux', architecture } })) }) };
    if (url.includes('/manifests/')) return { ok: true, json: async () => ({ config: { digest: 'sha256:config' } }) };
    return { ok: true, json: async () => ({ config: { Labels: { 'org.opencontainers.image.revision': wrongSha ? 'c'.repeat(40) : sha, 'org.opencontainers.image.version': '1.0.0' } } }) };
  } };
}
test('an existing matching multi-platform image is reused by immutable digest', async () => {
  assert.equal(await inspectImage(registry(), 'ghcr.io/owner/app', '1.0.0', sha), 'sha256:' + 'b'.repeat(64));
});
test('a missing image can be staged, while registry failures remain failures', async () => {
  assert.equal(await inspectImage(registry({ missing: true }), 'ghcr.io/owner/app', '1.0.0', sha), null);
  await assert.rejects(inspectImage(registry({ forbidden: true }), 'ghcr.io/owner/app', '1.0.0', sha), /HTTP 403/);
});
test('a conflicting versioned image is never accepted for replacement', async () => {
  await assert.rejects(inspectImage(registry({ wrongSha: true }), 'ghcr.io/owner/app', '1.0.0', sha), /another commit/);
});
