import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import semver from 'semver';
import { assetNames, RELEASE_BRANCH, validateChecksums } from './policy.mjs';
import { ciSuccess } from './dependencies.mjs';
import { output, summary } from './github.mjs';

const STATE_TITLE = 'Release automation state';
const STATE_MARKER = '<!-- dependency-release-state:v1 -->';
const RELEASE_MARKER = '<!-- dependency-release:v1';

export async function state(api) {
  const issues = await api.pages('/issues?state=all&creator=github-actions%5Bbot%5D');
  const matches = issues.filter(issue => !issue.pull_request && issue.title === STATE_TITLE && issue.user.login === 'github-actions[bot]'
    && issue.body?.startsWith(STATE_MARKER));
  assert(matches.length <= 1, 'Duplicate release state issues require administrator review');
  const issue = matches[0];
  if (!issue) return { issue: null, hold: false, initialVersion: null };
  const value = JSON.parse(issue.body.slice(STATE_MARKER.length).trim());
  assert(typeof value.hold === 'boolean', 'Invalid release hold state');
  return { ...value, issue };
}

export async function changeState(api, event, operation, initialVersion) {
  if (operation === 'reconcile' && !initialVersion) return state(api);
  assert(process.env.GITHUB_EVENT_NAME === 'workflow_dispatch', 'Manual state changes require workflow_dispatch');
  const actor = process.env.GITHUB_TRIGGERING_ACTOR || process.env.GITHUB_ACTOR;
  const permission = await api.get(`/collaborators/${encodeURIComponent(actor)}/permission`);
  assert(['maintain', 'admin'].includes(permission.permission) || permission.user?.permissions?.maintain || permission.user?.permissions?.admin,
    'Manual release operation requires maintain or admin permission');
  const current = await state(api);
  if (operation === 'preview') {
    assert(current.hold, 'Preview requires release hold');
    return current;
  }
  assert(['reconcile', 'hold', 'resume'].includes(operation), 'Unknown release operation');
  if (initialVersion) {
    assert(semver.valid(initialVersion) && !current.initialVersion, 'Initial version must be valid and may only be registered once');
  }
  const value = { hold: operation === 'hold' ? true : operation === 'resume' ? false : current.hold,
    initialVersion: initialVersion || current.initialVersion, actor, updatedAt: new Date().toISOString() };
  const body = STATE_MARKER + '\n' + JSON.stringify(value, null, 2);
  const issue = current.issue ? await api.mutate('PATCH', `/issues/${current.issue.number}`, { body, state: 'open' })
    : await api.mutate('POST', '/issues', { title: STATE_TITLE, body });
  return { ...value, issue };
}

async function mergedReleases(api, config) {
  const pulls = await api.pages('/pulls?state=closed&base=main&sort=updated&direction=desc');
  const result = [];
  for (const pr of pulls.filter(pr => pr.merged_at && pr.head.ref === RELEASE_BRANCH && pr.user.login === 'github-actions[bot]')) {
    const comparison = await api.get(`/compare/${config.bootstrap}...${pr.merge_commit_sha}`);
    if (comparison.status === 'behind' || comparison.status === 'identical') continue;
    assert(comparison.behind_by === 0, 'Managed release is not descended from the bootstrap commit');
    const manifest = JSON.parse(await api.content('.release-please-manifest.json', pr.merge_commit_sha));
    const mergeCommit = await api.get(`/git/commits/${pr.merge_commit_sha}`);
    assert(mergeCommit.parents.length > 0, 'Release merge commit has no parent');
    const previous = JSON.parse(await api.content('.release-please-manifest.json', mergeCommit.parents[0].sha));
    const version = manifest['.'];
    assert(semver.valid(version) && semver.gt(version, previous['.']), 'Merged release PR has an invalid version change');
    const full = await api.get(`/pulls/${pr.number}`);
    assert(full.head.repo.full_name === api.repository && full.base.ref === 'main', 'Managed release must be from the same repository');
    const files = await api.pages(`/pulls/${pr.number}/files`);
    const allowed = ['CHANGELOG.md', '.release-please-manifest.json', ...(config.versionFiles || [])];
    assert(files.length === full.changed_files && files.every(file => allowed.includes(file.filename)), 'Release PR changed files outside the configured version files');
    result.push({ pr: full, version, tag: `v${version}`, sha: pr.merge_commit_sha });
  }
  return result.sort((a, b) => new Date(a.pr.merged_at) - new Date(b.pr.merged_at));
}

export async function tagSha(api, tag) {
  let ref = await api.optional(`/git/ref/tags/${encodeURIComponent(tag)}`);
  if (!ref) return null;
  for (let depth = 0; ref.object.type === 'tag'; depth++) {
    assert(depth < 5, 'Nested annotated tag limit exceeded');
    ref = await api.get(`/git/tags/${ref.object.sha}`);
  }
  assert(ref.object.type === 'commit', 'Release tag must point to a commit');
  return ref.object.sha;
}

function notes(changelog, version) {
  const parts = changelog.split(/(?=^##\s)/m);
  const section = parts.find(part => new RegExp(`^##\\s+\\[?${version.replaceAll('.', '\\.')}[\\]\\s(]`).test(part));
  assert(section?.trim(), 'Version is absent from CHANGELOG.md');
  return section.trim();
}

async function marker(release, candidate) {
  const match = release.body?.match(/<!-- dependency-release:v1 (\{[^\n]*\}) -->/);
  if (match) {
    const stored = JSON.parse(match[1]);
    assert(stored.pr === candidate.pr.number && stored.sha === candidate.sha && stored.tag === candidate.tag, 'Draft release ownership record does not match the merged PR');
  }
  return match;
}

export async function pending(api, config, create = false) {
  const candidates = await mergedReleases(api, config);
  const releases = await api.pages('/releases');
  const unfinished = [];
  for (const candidate of candidates) {
    let release = releases.find(release => release.tag_name === candidate.tag);
    const sha = await tagSha(api, candidate.tag);
    if (sha) assert(sha === candidate.sha, `Tag ${candidate.tag} does not match the release PR merge SHA`);
    if (release && !release.draft) {
      assert(sha === candidate.sha, 'Published release tag is missing or mismatched');
      if (create) await markPublished(api, candidate.pr);
      continue;
    }
    assert(await ciSuccess(api, candidate.pr, config), 'Merged release PR head did not pass required CI');
    if (release) {
      assert(release.author.login === 'github-actions[bot]', 'Unmanaged draft release has the expected version tag; refusing to modify it');
      assert(sha === candidate.sha, 'Draft release tag is missing or mismatched');
      await marker(release, candidate);
    }
    if (create && !release) {
      if (!sha) await api.mutate('POST', '/git/refs', { ref: `refs/tags/${candidate.tag}`, sha: candidate.sha });
      release = await api.mutate('POST', '/releases', { tag_name: candidate.tag, target_commitish: candidate.sha,
        name: candidate.tag, body: notes(await api.content('CHANGELOG.md', candidate.sha), candidate.version), draft: true,
        prerelease: Boolean(semver.prerelease(candidate.version)) });
    }
    if (create && release && !await marker(release, candidate)) {
      const ownership = `${RELEASE_MARKER} ${JSON.stringify({ pr: candidate.pr.number, sha: candidate.sha, tag: candidate.tag })} -->`;
      release = await api.mutate('PATCH', `/releases/${release.id}`, { body: `${release.body || ''}\n\n${ownership}` });
    }
    unfinished.push({ ...candidate, release });
  }
  assert(unfinished.length <= 1, 'Multiple unfinished releases require administrator review');
  return unfinished[0] || null;
}

async function markPublished(api, pr) {
  if (!pr.labels?.some(label => label.name === 'autorelease: tagged')) {
    await api.mutate('POST', `/issues/${pr.number}/labels`, { labels: ['autorelease: tagged'] });
  }
  if (pr.labels?.some(label => label.name === 'autorelease: pending')) {
    await api.mutate('DELETE', `/issues/${pr.number}/labels/${encodeURIComponent('autorelease: pending')}`);
  }
}

export async function ready(api, config, holdState) {
  const unfinished = await pending(api, config);
  const pulls = await api.pages('/pulls?state=open&base=main');
  for (const pr of pulls) {
    const isRelease = pr.user.login === 'github-actions[bot]' && pr.head.ref === RELEASE_BRANCH;
    const reason = isRelease ? holdState.hold ? 'Release is on hold' : unfinished ? 'A previous release is unfinished' : 'Release may be reviewed and merged' : 'Not a release PR';
    await api.check(pr.head.sha, 'release-ready', isRelease && (holdState.hold || unfinished) ? 'action_required' : 'success', { pr: pr.number, sha: pr.head.sha, reason });
    if (isRelease && holdState.hold && !pr.draft) await api.graphql('mutation($id:ID!){convertPullRequestToDraft(input:{pullRequestId:$id}){pullRequest{id}}}', { id: pr.node_id });
  }
}

export async function prepare(api, config, event, operation, initialVersion) {
  if (initialVersion) {
    const manifest = JSON.parse(await api.content('.release-please-manifest.json', 'main'));
    assert(config.requiresInitialVersion && initialVersion === manifest['.'], 'Confirm the configured initial manifest version, or update it in a reviewed PR first');
  }
  const holdState = await changeState(api, event, operation, initialVersion);
  await ready(api, config, holdState);
  const unfinished = holdState.hold ? null : await pending(api, config, true);
  output('pending', Boolean(unfinished));
  if (unfinished?.release) emitRelease(unfinished);
  const initialized = !config.requiresInitialVersion || Boolean(holdState.initialVersion);
  output('run-release-please', initialized && ((!holdState.hold && !unfinished) || operation === 'preview'));
  output('skip-github-release', holdState.hold);
  if (!initialized) summary('Release automation awaits a maintainer workflow_dispatch with initial-version. Dependency updates and CI remain available.');
  if (holdState.hold) summary('Release hold is active. No tag, asset, or release will be published.');
}

export async function discover(api, config) {
  const holdState = await state(api);
  assert(!holdState.hold, 'Release hold is active');
  const unfinished = await pending(api, config, true);
  if (unfinished) emitRelease(unfinished);
  output('pending', Boolean(unfinished));
  await ready(api, config, holdState);
}

function emitRelease(item) {
  assert(item.release?.id, 'Managed draft release is missing');
  output('release-id', item.release.id);
  output('tag', item.tag);
  output('sha', item.sha);
}

async function assetBytes(api, asset) {
  const response = await api.fetcher(`https://api.github.com${api.root}/releases/assets/${asset.id}`, {
    headers: { authorization: `Bearer ${api.token}`, accept: 'application/octet-stream', 'X-GitHub-Api-Version': '2022-11-28' },
  });
  assert(response.ok, `Asset download failed: ${asset.name}`);
  return Buffer.from(await response.arrayBuffer());
}

export async function verifyAssets(api, release, config, version) {
  const expected = assetNames(config, version);
  const assets = await api.pages(`/releases/${release.id}/assets`);
  for (const name of expected) assert(assets.some(asset => asset.name === name && asset.size > 0 && asset.state === 'uploaded'), `Missing or empty asset: ${name}`);
  if (expected.length) {
    const checksumAsset = assets.find(asset => asset.name === 'checksums.txt');
    assert(checksumAsset, 'Missing checksums.txt');
    const names = expected.filter(name => name !== 'checksums.txt');
    const checksums = validateChecksums((await assetBytes(api, checksumAsset)).toString('utf8'), names);
    for (const name of names) {
      const data = await assetBytes(api, assets.find(asset => asset.name === name));
      assert(createHash('sha256').update(data).digest('hex') === checksums.get(name), `Checksum mismatch: ${name}`);
    }
  }
}

export async function finalize(api, config, id, tag, sha) {
  assert(!(await state(api)).hold, 'Release hold is active');
  const unfinished = await pending(api, config);
  assert(unfinished && unfinished.release.id === Number(id) && unfinished.tag === tag && unfinished.sha === sha, 'Release inputs do not match the unfinished managed release');
  assert(await tagSha(api, tag) === sha, 'Tag changed before publication');
  const release = await api.get(`/releases/${id}`);
  assert(release.draft, 'Release is already public; refusing to modify it');
  await verifyAssets(api, release, config, unfinished.version);
  assert(!(await state(api)).hold, 'Release hold was enabled before publication');
  await api.mutate('PATCH', `/releases/${id}`, { draft: false, make_latest: semver.prerelease(unfinished.version) ? 'false' : 'true' });
  await markPublished(api, unfinished.pr);
  await ready(api, config, await state(api));
  summary(`Published ${tag} from release PR #${unfinished.pr.number}, merge ${sha}.`);
}

export async function verifyDistribution(api, config, id, tag, sha) {
  assert(!(await state(api)).hold, 'Release hold is active');
  const unfinished = await pending(api, config);
  assert(unfinished?.release?.id === Number(id) && unfinished.tag === tag && unfinished.sha === sha,
    'Distribution inputs do not match the unfinished managed release');
  assert(await tagSha(api, tag) === sha, 'Release tag does not match distribution SHA');
}
