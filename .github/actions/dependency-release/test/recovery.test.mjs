import test from 'node:test';
import assert from 'node:assert/strict';
import { pending, ready, verifyAssets } from '../src/releases.mjs';
import { createHash } from 'node:crypto';

const merge = 'a'.repeat(40), head = 'b'.repeat(40), parent = 'c'.repeat(40);
const pr = { number: 7, node_id: 'PR_7', merged_at: '2026-11-14T00:00:00Z', merge_commit_sha: merge,
  changed_files: 2, user: { login: 'github-actions[bot]' }, head: { ref: 'release-please--branches--main', sha: head, repo: { full_name: 'o/r' } }, base: { ref: 'main' } };
const config = { bootstrap: parent, ci: 'ci.yml', ciJobs: ['tests'], assets: [], versionFiles: [] };
function fixture(options = {}) {
  const writes = [];
  const release = { id: 9, tag_name: 'v0.2.1', draft: true, author: { login: 'github-actions[bot]' }, body: '## 0.2.1\nFix\n' };
  const api = { repository: 'o/r', writes,
    async pages(path) {
      if (path.startsWith('/pulls?state=closed')) return [pr];
      if (path.startsWith('/pulls?state=open')) return [{ ...pr, merged_at: null }];
      if (path === '/releases') return options.noRelease ? [] : [release];
      if (path.endsWith('/files')) return [{ filename: 'CHANGELOG.md' }, { filename: '.release-please-manifest.json' }];
      if (path.includes('/runs?')) return [{ id: 1, head_sha: head, event: 'pull_request', head_repository: { full_name: 'o/r' }, status: 'completed', conclusion: 'success', run_attempt: 1 }];
      if (path.endsWith('/jobs')) return [{ name: 'ci', conclusion: 'success' }, { name: 'tests', conclusion: 'success' }];
      throw new Error(path);
    },
    async get(path) {
      if (path.startsWith('/compare/')) return { status: 'ahead', behind_by: 0 };
      if (path.startsWith('/git/commits/')) return { parents: [{ sha: parent }] };
      if (path === '/pulls/7') return pr;
      throw new Error(path);
    },
    async content(path, ref) {
      if (path === '.release-please-manifest.json') return JSON.stringify({ '.': ref === parent ? '0.2.0' : '0.2.1' });
      if (path === 'CHANGELOG.md') return '# Changelog\n\n## [0.2.1](https://example.com)\nFix\n';
      throw new Error(path);
    },
    async optional() { return options.noTag ? null : { object: { type: 'commit', sha: options.wrongSha ? head : merge } }; },
    async mutate(method, path, body) { writes.push({ method, path, body }); return { ...release, ...body }; },
    async check(sha, name, conclusion) { writes.push({ sha, name, conclusion }); },
    async graphql() { throw new Error('Unexpected GraphQL mutation'); },
  };
  return api;
}

test('recovery reuses the existing draft ID and adds ownership after label failure', async () => {
  const api = fixture();
  const result = await pending(api, config, true);
  assert.equal(result.release.id, 9);
  assert.equal(api.writes.filter(write => write.method === 'POST' && write.path === '/releases').length, 0);
  assert.match(api.writes[0].body.body, /dependency-release:v1/);
});
test('a missing draft is recovered at the merged release PR SHA', async () => {
  const api = fixture({ noRelease: true, noTag: true });
  const result = await pending(api, config, true);
  assert.equal(result.sha, merge);
  assert.deepEqual(api.writes.find(write => write.path === '/git/refs').body, { ref: 'refs/tags/v0.2.1', sha: merge });
  assert.equal(api.writes.find(write => write.path === '/releases').body.draft, true);
});
test('a mismatched tag stops recovery without changing the tag or draft', async () => {
  const api = fixture({ wrongSha: true });
  await assert.rejects(pending(api, config, true), /does not match/);
  assert.equal(api.writes.length, 0);
});
test('release-ready blocks a new release PR while a previous publication is unfinished', async () => {
  const api = fixture();
  await ready(api, config, { hold: false });
  assert.equal(api.writes[0].conclusion, 'action_required');
});
test('source-only distribution has no assets to upload', async () => {
  await verifyAssets({ pages: async () => [] }, { id: 9 }, { assets: [] }, '0.2.1');
});
test('a missing asset and a checksum mismatch prevent publication', async () => {
  await assert.rejects(verifyAssets({ pages: async () => [] }, { id: 9 }, { assets: ['app.zip', 'checksums.txt'] }, '0.2.1'), /Missing or empty asset/);
  const bytes = Buffer.from('app');
  const assets = [{ id: 1, name: 'app.zip', size: 3, state: 'uploaded' }, { id: 2, name: 'checksums.txt', size: 74, state: 'uploaded' }];
  const hash = createHash('sha256').update('different').digest('hex');
  const api = { root: '/repos/o/r', token: 'test', pages: async () => assets, fetcher: async url => ({ ok: true, arrayBuffer: async () => url.endsWith('/2') ? Buffer.from(`${hash}  app.zip`) : bytes }) };
  await assert.rejects(verifyAssets(api, { id: 9 }, { assets: ['app.zip', 'checksums.txt'] }, '0.2.1'), /Checksum mismatch/);
});
