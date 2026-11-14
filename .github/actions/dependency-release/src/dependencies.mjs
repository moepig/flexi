import assert from 'node:assert/strict';
import { automaticUpdate, CHECK_APP_ID, CHECKS, metadata, RELEASE_BRANCH, releaseImpact, squash, validateFile } from './policy.mjs';
import { summary } from './github.mjs';

export async function protection(api) {
  const rules = await api.get('/rules/branches/main');
  const status = rules.find(rule => rule.type === 'required_status_checks')?.parameters;
  const reviews = rules.find(rule => rule.type === 'pull_request')?.parameters;
  assert(status?.strict_required_status_checks_policy, 'main must require an up-to-date branch');
  for (const name of CHECKS) assert(status.required_status_checks.some(check => check.context === name
    && check.integration_id === CHECK_APP_ID), `main must require GitHub Actions check: ${name}`);
  assert(reviews?.required_approving_review_count >= 1 && reviews.dismiss_stale_reviews_on_push,
    'main must require review and dismiss stale approvals');
  return true;
}

export async function ciSuccess(api, pr, config) {
  const runs = await api.pages(`/actions/workflows/${config.ci}/runs?event=pull_request&head_sha=${pr.head.sha}`, 'workflow_runs');
  const latest = runs.filter(run => run.head_sha === pr.head.sha && run.event === 'pull_request'
    && run.head_repository?.full_name === api.repository).sort((a, b) => b.id - a.id)[0];
  if (!latest || latest.status !== 'completed' || latest.conclusion !== 'success') return false;
  const jobs = await api.pages(`/actions/runs/${latest.id}/attempts/${latest.run_attempt}/jobs`, 'jobs');
  return jobs.some(job => job.name === 'ci' && job.conclusion === 'success')
    && config.ciJobs.every(name => jobs.some(job => job.name === name && job.conclusion === 'success'));
}

async function latestApproval(api, pr) {
  const reviews = await api.pages(`/pulls/${pr.number}/reviews`);
  const latest = new Map();
  for (const review of reviews) if (review.state !== 'COMMENTED' && review.state !== 'PENDING') latest.set(review.user.login, review);
  for (const review of latest.values()) {
    if (review.state !== 'APPROVED' || review.commit_id !== pr.head.sha || review.user.type === 'Bot') continue;
    const permission = await api.get(`/collaborators/${encodeURIComponent(review.user.login)}/permission`);
    if (['admin', 'maintain'].includes(permission.permission) || permission.user?.permissions?.maintain || permission.user?.permissions?.admin) return review;
  }
  return null;
}

async function autoMerge(api, pr, value) {
  const result = await api.graphql('query($id:ID!){node(id:$id){... on PullRequest{headRefOid autoMergeRequest{commitHeadline commitBody}}}}', { id: pr.node_id });
  assert(result.node, 'PR no longer exists');
  if (value) assert(result.node.headRefOid === pr.head.sha, 'PR head changed before auto-merge');
  if (result.node.autoMergeRequest) await api.graphql('mutation($id:ID!){disablePullRequestAutoMerge(input:{pullRequestId:$id}){pullRequest{id}}}', { id: pr.node_id });
  if (!value) return;
  const current = await api.get(`/pulls/${pr.number}`);
  assert(current.head.sha === pr.head.sha && current.state === 'open', 'PR head changed before auto-merge');
  await api.graphql('mutation($id:ID!,$title:String!,$body:String!,$sha:GitObjectID!){enablePullRequestAutoMerge(input:{pullRequestId:$id,mergeMethod:SQUASH,commitHeadline:$title,commitBody:$body,expectedHeadOid:$sha}){pullRequest{id}}}',
    { id: pr.node_id, title: value.title, body: value.body, sha: pr.head.sha });
}

export async function evaluate(api, pr, config) {
  const record = { pr: pr.number, sha: pr.head.sha, reasons: [], dependencies: [], releaseImpact: true };
  let check;
  try {
    check = await api.check(pr.head.sha, 'dependency-review', 'action_required', { ...record, reasons: ['Evaluation in progress'] });
    assert(pr.base.ref === 'main' && pr.base.repo.full_name === api.repository && pr.head.repo.full_name === api.repository, 'PR must target main from the same repository');
    assert(pr.head.ref.startsWith('dependabot/') && pr.user.login === 'dependabot[bot]' && pr.user.type === 'Bot', 'Dependabot author and branch are required');
    const commits = await api.pages(`/pulls/${pr.number}/commits`);
    assert(commits.length > 0 && commits.at(-1).sha === pr.head.sha, 'PR commits do not match its head');
    assert(commits.every(commit => commit.author?.login === 'dependabot[bot]' && commit.commit.verification?.verified), 'Every commit must be signed and authored by Dependabot');
    record.dependencies = commits.flatMap(commit => metadata(commit.commit.message));
    const files = await api.pages(`/pulls/${pr.number}/files`);
    assert(files.length === pr.changed_files && files.length <= 100, 'Incomplete or oversized PR diff');
    const changes = [];
    for (const file of files) {
      assert(file.status === 'modified', 'File creation, deletion, and renames require manual review');
      const [before, after] = await Promise.all([api.content(file.filename, pr.base.sha), api.content(file.filename, pr.head.sha)]);
      changes.push(validateFile(file.filename, before, after, config));
    }
    record.releaseImpact = releaseImpact(record.dependencies, changes, config.noRelease);
    record.squash = squash(record.dependencies, record.releaseImpact, pr.number, pr.head.sha);
    const breaking = pr.labels.some(label => label.name === 'breaking-change') || /BREAKING CHANGE:|^[a-z]+(?:\([^)]*\))?!:/.test(pr.title + '\n' + (pr.body || ''));
    const automatic = automaticUpdate(record.dependencies, config.allowMajor) && !breaking;
    record.automatic = automatic;
    const approval = automatic ? null : await latestApproval(api, pr);
    if (!automatic) {
      await autoMerge(api, pr, null);
      record.reasons.push(approval ? 'Maintainer approved the current head; squash manually using the recorded message' : 'Current head requires maintainer approval');
      if (!approval) return await finish('action_required');
      if (breaking) {
        const match = pr.body?.match(/(?:^|\n)BREAKING CHANGE:\s*(.+)/);
        assert(match, 'Add BREAKING CHANGE: with the product compatibility change to the PR body');
        record.squash = squash(record.dependencies, record.releaseImpact, pr.number, pr.head.sha, match[1]);
      }
    }
    assert(await ciSuccess(api, pr, config), 'Current head has not passed every required CI job');
    const comparison = await api.get(`/compare/main...${pr.head.sha}`);
    assert(comparison.behind_by === 0 && pr.mergeable === true, 'Update the PR branch against main and rerun CI');
    if (automatic) {
      await protection(api);
      assert((await api.get('')).allow_auto_merge, 'Repository auto-merge is disabled');
      const current = await api.get(`/pulls/${pr.number}`);
      assert(current.head.sha === pr.head.sha && current.base.sha === pr.base.sha && current.state === 'open' && !current.draft, 'PR changed during evaluation');
      const reviews = await api.pages(`/pulls/${pr.number}/reviews`);
      if (!reviews.some(review => review.user.login === 'github-actions[bot]' && review.state === 'APPROVED' && review.commit_id === pr.head.sha)) {
        await api.mutate('POST', `/pulls/${pr.number}/reviews`, { event: 'APPROVE', commit_id: pr.head.sha, body: `Dependency policy passed for ${pr.head.sha}.` });
      }
      await autoMerge(api, pr, record.squash);
      record.reasons.push('Signed dependency update, current CI, protected main, and squash reservation verified');
    }
    return await finish('success');
  } catch (error) {
    record.reasons.push(error.message);
    try { await autoMerge(api, pr, null); } catch (cancelError) { record.reasons.push(`Could not cancel auto-merge: ${cancelError.message}`); }
    return await finish('action_required');
  }
  async function finish(conclusion) {
    if (check) await api.mutate('PATCH', `/check-runs/${check.id}`, {
      status: 'completed', conclusion, output: { title: `dependency-review: ${conclusion}`, summary: `PR #${pr.number}, head ${pr.head.sha}`, text: JSON.stringify(record, null, 2) },
    });
    summary(`Dependency PR #${pr.number}: ${conclusion}; ${record.reasons.join('; ')}`);
    return record;
  }
}

export async function dependencies(api, config, event) {
  const pulls = event.pull_request ? [event.pull_request] : await api.pages('/pulls?state=open&base=main');
  for (const listed of pulls) {
    const pr = await api.get(`/pulls/${listed.number}`);
    if (pr.state !== 'open' || pr.base.ref !== 'main') continue;
    if (pr.user.login !== 'dependabot[bot]') {
      await api.check(pr.head.sha, 'dependency-review', 'success', { pr: pr.number, sha: pr.head.sha, reason: 'Not a Dependabot PR' });
    } else if (pr.draft) {
      await autoMerge(api, pr, null);
      await api.check(pr.head.sha, 'dependency-review', 'action_required', { pr: pr.number, reason: 'Draft PR' });
    } else await evaluate(api, pr, config);
  }
}
