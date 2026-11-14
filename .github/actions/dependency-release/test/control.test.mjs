import test from 'node:test';
import assert from 'node:assert/strict';
import { ciSuccess, evaluate, protection } from '../src/dependencies.mjs';
import { GitHub } from '../src/github.mjs';
import { changeState, pending, tagSha } from '../src/releases.mjs';

const sha = 'a'.repeat(40);
const pr = { number: 1, node_id: 'PR_1', state: 'open', draft: false, changed_files: 1, user: { login: 'dependabot[bot]', type: 'Bot' },
  head: { sha, ref: 'dependabot/go_modules/a-1.0.1', repo: { full_name: 'owner/repo' } },
  base: { sha: 'b'.repeat(40), ref: 'main', repo: { full_name: 'owner/repo' } }, labels: [], title: 'Bump a', body: '' };
const config = { ci: 'ci.yml', ciJobs: ['tests'], files: ['go.mod'], actions: [], bootstrap: 'b'.repeat(40), assets: [] };

test('API errors remain failures rather than empty security/update information', async () => {
  const api = new GitHub('test', 'owner/repo', async () => ({ ok: false, status: 403 }));
  await assert.rejects(api.pages('/pulls'), /HTTP 403/);
  await assert.rejects(api.optional('/pulls/1'), /HTTP 403/);
});
test('a later failed CI run overrides an earlier successful run', async () => {
  const api = { repository: 'owner/repo', async pages(path) {
    if (path.includes('/runs?')) return [
      { id: 1, head_sha: sha, event: 'pull_request', head_repository: { full_name: 'owner/repo' }, status: 'completed', conclusion: 'success' },
      { id: 2, head_sha: sha, event: 'pull_request', head_repository: { full_name: 'owner/repo' }, status: 'completed', conclusion: 'failure' },
    ];
    throw new Error('Unexpected jobs request');
  } };
  assert.equal(await ciSuccess(api, pr, config), false);
});
test('missing and skipped required jobs block dependency merging', async () => {
  const api = { repository: 'owner/repo', async pages(path) {
    if (path.includes('/runs?')) return [{ id: 1, run_attempt: 1, head_sha: sha, event: 'pull_request', head_repository: { full_name: 'owner/repo' }, status: 'completed', conclusion: 'success' }];
    return [{ name: 'ci', conclusion: 'success' }, { name: 'tests', conclusion: 'skipped' }];
  } };
  assert.equal(await ciSuccess(api, pr, config), false);
});
test('missing branch protection, wrong check provider, and absent review rules block automation', async () => {
  await assert.rejects(protection({ get: async () => [] }), /up-to-date/);
  await assert.rejects(protection({ get: async () => [{ type: 'required_status_checks', parameters: {
    strict_required_status_checks_policy: true, required_status_checks: [{ context: 'ci', integration_id: 999 }],
  } }] }), /GitHub Actions check/);
});
test('PR head changes invalidate auto-merge and never record success', async () => {
  const updates = [];
  const api = { repository: 'owner/repo', check: async () => ({ id: 1 }),
    async pages(path) {
      if (path.endsWith('/commits')) return [{ sha, author: { login: 'dependabot[bot]' }, commit: { verification: { verified: true }, message: 'Bump a\n---\nupdated-dependencies:\n- dependency-name: a\n  update-type: version-update:semver-patch\n...\n' } }];
      if (path.endsWith('/files')) return [{ filename: 'go.mod', status: 'modified' }];
      if (path.includes('/runs?')) return [];
      throw new Error(path);
    },
    content: async () => 'module example.com/a\ngo 1.27.3\nrequire a v1.0.0',
    graphql: async () => ({ node: { headRefOid: 'c'.repeat(40), autoMergeRequest: null } }),
    mutate: async (...args) => { updates.push(args); },
  };
  const result = await evaluate(api, pr, config);
  assert(result.reasons.some(reason => reason.includes('CI')));
  assert(updates.every(([, , body]) => body.conclusion !== 'success'));
});
test('unverified commits cannot enable auto-merge', async () => {
  const writes = [];
  const api = { repository: 'owner/repo', check: async () => ({ id: 1 }), pages: async () => [{ sha, author: { login: 'dependabot[bot]' }, commit: { verification: { verified: false } } }],
    graphql: async () => ({ node: { headRefOid: sha, autoMergeRequest: null } }), mutate: async (...args) => writes.push(args) };
  const record = await evaluate(api, pr, config);
  assert(record.reasons.some(reason => reason.includes('signed')));
  assert(writes.every(([, , body]) => body.conclusion === 'action_required'));
});
test('tag verification resolves annotated tags and detects missing tags', async () => {
  assert.equal(await tagSha({ optional: async () => null }, 'v1.0.0'), null);
  assert.equal(await tagSha({ optional: async () => ({ object: { type: 'tag', sha: 'b'.repeat(40) } }), get: async () => ({ object: { type: 'commit', sha } }) }, 'v1.0.0'), sha);
});
test('release recovery ignores unrelated drafts when no managed PR was merged', async () => {
  const api = { pages: async path => path.startsWith('/pulls') ? [] : [{ id: 1, tag_name: 'v9.9.9', draft: true }] };
  assert.equal(await pending(api, config, true), null);
});
test('hold/resume require workflow_dispatch and maintainer permission', async () => {
  const previous = process.env.GITHUB_EVENT_NAME;
  process.env.GITHUB_EVENT_NAME = 'push';
  await assert.rejects(changeState({}, {}, 'hold', ''), /workflow_dispatch/);
  process.env.GITHUB_EVENT_NAME = 'workflow_dispatch';
  await assert.rejects(changeState({ get: async () => ({ permission: 'write' }) }, {}, 'hold', ''), /maintain or admin/);
  if (previous === undefined) delete process.env.GITHUB_EVENT_NAME; else process.env.GITHUB_EVENT_NAME = previous;
});

function mergeFixture(updateType = 'version-update:semver-patch', approvalSha = null) {
  const writes = [];
  const current = { ...pr, mergeable: true };
  const api = { repository: 'owner/repo', writes,
    check: async () => ({ id: 1 }),
    content: async (_, ref) => `module example.com/a\ngo 1.27.3\nrequire a ${ref === sha ? 'v1.0.1' : 'v1.0.0'}`,
    async get(path) {
      if (path === '') return { allow_auto_merge: true };
      if (path === '/pulls/1') return current;
      if (path.startsWith('/collaborators/')) return { permission: 'admin' };
      if (path.startsWith('/compare/')) return { behind_by: 0 };
      if (path === '/rules/branches/main') return [
        { type: 'required_status_checks', parameters: { strict_required_status_checks_policy: true, required_status_checks: ['ci', 'dependency-review', 'release-ready'].map(context => ({ context, integration_id: 15368 })) } },
        { type: 'pull_request', parameters: { required_approving_review_count: 1, dismiss_stale_reviews_on_push: true } },
      ];
      throw new Error(path);
    },
    async pages(path) {
      if (path.endsWith('/commits')) return [{ sha, author: { login: 'dependabot[bot]' }, commit: { verification: { verified: true }, message: `Bump a\n---\nupdated-dependencies:\n- dependency-name: a\n  update-type: ${updateType}\n...\n` } }];
      if (path.endsWith('/files')) return [{ filename: 'go.mod', status: 'modified' }];
      if (path.includes('/runs?')) return [{ id: 1, run_attempt: 1, head_sha: sha, event: 'pull_request', head_repository: { full_name: 'owner/repo' }, status: 'completed', conclusion: 'success' }];
      if (path.endsWith('/jobs')) return [{ name: 'ci', conclusion: 'success' }, { name: 'tests', conclusion: 'success' }];
      if (path.endsWith('/reviews')) return approvalSha ? [{ user: { login: 'maintainer', type: 'User' }, state: 'APPROVED', commit_id: approvalSha }] : [];
      throw new Error(path);
    },
    async graphql(query) { writes.push({ query }); return query.startsWith('query') ? { node: { headRefOid: sha, autoMergeRequest: null } } : {}; },
    async mutate(method, path, body) { writes.push({ method, path, body }); },
  };
  return api;
}
test('a current protected and tested patch reserves the exact squash before the success check', async () => {
  const api = mergeFixture();
  const record = await evaluate(api, { ...pr, mergeable: true }, config);
  assert.equal(record.automatic, true);
  assert.match(record.squash.title, /^fix\(deps\):/);
  const reserve = api.writes.findIndex(write => write.query?.includes('enablePullRequestAutoMerge'));
  const success = api.writes.findIndex(write => write.body?.conclusion === 'success');
  assert(reserve >= 0 && success > reserve);
});
test('major updates never reserve auto-merge and require approval of the exact current SHA', async () => {
  const stale = mergeFixture('version-update:semver-major', 'c'.repeat(40));
  await evaluate(stale, { ...pr, mergeable: true }, config);
  assert(!stale.writes.some(write => write.body?.conclusion === 'success'));
  const approved = mergeFixture('version-update:semver-major', sha);
  await evaluate(approved, { ...pr, mergeable: true }, config);
  assert(approved.writes.some(write => write.body?.conclusion === 'success'));
  assert(!approved.writes.some(write => write.query?.includes('enablePullRequestAutoMerge')));
  assert(!approved.writes.some(write => write.body?.event === 'APPROVE'));
});
