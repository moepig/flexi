import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import semver from 'semver';
import { state, tagSha, verifyAssets } from './releases.mjs';

const ACCEPT = 'application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json';

export async function inspectImage(api, image, version, sha) {
  assert(/^ghcr\.io\/[a-z0-9._-]+\/[a-z0-9._-]+$/.test(image), 'Only configured GHCR repositories are supported');
  const name = image.slice('ghcr.io/'.length);
  const auth = await api.fetcher(`https://ghcr.io/token?service=ghcr.io&scope=${encodeURIComponent(`repository:${name}:pull`)}`, {
    headers: { authorization: `Basic ${Buffer.from(`${process.env.GITHUB_ACTOR}:${api.token}`).toString('base64')}` },
  });
  assert(auth.ok, 'GHCR authentication failed');
  const token = (await auth.json()).token;
  assert(token, 'GHCR bearer token is missing');
  const headers = { authorization: `Bearer ${token}`, accept: ACCEPT };
  const response = await api.fetcher(`https://ghcr.io/v2/${name}/manifests/${version}`, { headers });
  if (response.status === 404) return null;
  assert(response.ok, `Could not inspect ${image}:${version}: HTTP ${response.status}`);
  const digest = response.headers.get('docker-content-digest');
  assert(/^sha256:[a-f0-9]{64}$/.test(digest), 'Registry returned an invalid manifest digest');
  const index = await response.json();
  const platforms = index.manifests?.filter(item => item.platform?.os === 'linux' && ['amd64', 'arm64'].includes(item.platform.architecture));
  assert(platforms?.some(item => item.platform.architecture === 'amd64') && platforms.some(item => item.platform.architecture === 'arm64'), 'Container must include linux/amd64 and linux/arm64');
  for (const platform of platforms) {
    const manifestResponse = await api.fetcher(`https://ghcr.io/v2/${name}/manifests/${platform.digest}`, { headers });
    assert(manifestResponse.ok, 'Platform manifest download failed');
    const manifest = await manifestResponse.json();
    const configResponse = await api.fetcher(`https://ghcr.io/v2/${name}/blobs/${manifest.config.digest}`, { headers });
    assert(configResponse.ok, 'Container config download failed');
    const config = await configResponse.json();
    assert(config.config?.Labels?.['org.opencontainers.image.revision'] === sha
      && config.config?.Labels?.['org.opencontainers.image.version'] === version, 'Existing versioned image belongs to another commit or version');
  }
  return digest;
}

export async function containers(api, config, id, tag, sha) {
  assert(!(await state(api)).hold, 'Release hold is active');
  assert(await tagSha(api, tag) === sha, 'Release tag does not match distribution commit');
  assert(execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim() === sha, 'Distribution checkout does not match release commit');
  const release = await api.get(`/releases/${id}`);
  assert(release.draft && release.tag_name === tag, 'Container distribution requires the existing draft release');
  const version = tag.slice(1);
  const artifacts = JSON.parse(readFileSync('dist/artifacts.json', 'utf8'));
  for (const container of config.containers || []) {
    if (await inspectImage(api, container.image, version, sha)) continue;
    const context = mkdtempSync(path.join(tmpdir(), 'release-container-'));
    try {
      for (const arch of ['amd64', 'arm64']) {
        const artifact = artifacts.find(item => item.type === 'Binary' && item.goos === 'linux' && item.goarch === arch && item.extra?.ID === container.id);
        assert(artifact, `Missing ${container.id} linux/${arch} binary`);
        const binary = path.resolve(artifact.path);
        assert(binary.startsWith(path.resolve('dist') + path.sep), 'Binary artifact is outside dist/');
        const dir = path.join(context, 'linux', arch);
        mkdirSync(dir, { recursive: true });
        copyFileSync(binary, path.join(dir, container.binary));
      }
      copyFileSync(container.dockerfile, path.join(context, 'Dockerfile'));
      assert(!(await state(api)).hold, 'Release hold is active');
      assert(!await inspectImage(api, container.image, version, sha), 'Versioned image appeared during staging; retry without replacing it');
      execFileSync('docker', ['buildx', 'build', '--platform', 'linux/amd64,linux/arm64', '--provenance=false', '--label', `org.opencontainers.image.revision=${sha}`,
        '--label', `org.opencontainers.image.version=${version}`, '--label', `org.opencontainers.image.source=https://github.com/${api.repository}`,
        '--tag', `${container.image}:${version}`, '--push', context], { stdio: 'inherit' });
      assert(await inspectImage(api, container.image, version, sha), 'Published versioned image was not found');
    } finally { rmSync(context, { recursive: true, force: true }); }
  }
  await verifyAssets(api, release, config, version);
  for (const container of config.containers || []) assert(await inspectImage(api, container.image, version, sha), 'Versioned container verification failed');
  if (semver.prerelease(version)) return;
  for (const container of config.containers || []) {
    assert(!(await state(api)).hold, 'Release hold is active');
    const digest = await inspectImage(api, container.image, version, sha);
    execFileSync('docker', ['buildx', 'imagetools', 'create', '--tag', `${container.image}:latest`, `${container.image}@${digest}`], { stdio: 'inherit' });
  }
}
