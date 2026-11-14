import assert from 'node:assert/strict';
import { isDeepStrictEqual } from 'node:util';
import { parse } from 'yaml';
import semver from 'semver';
import path from 'node:path';

export const RELEASE_BRANCH = 'release-please--branches--main';
export const CHECK_APP_ID = 15368;
export const CHECKS = ['ci', 'dependency-review', 'release-ready'];
export const MINOR_PATCH = ['version-update:semver-minor', 'version-update:semver-patch'];

export function metadata(message) {
  const fragment = message.match(/^---\r?\n([\s\S]*?)^\.\.\.\s*$/m);
  assert(fragment, 'Signed Dependabot metadata is missing');
  const data = parse(fragment[1], { uniqueKeys: true });
  const deps = data?.['updated-dependencies'];
  assert(Array.isArray(deps) && deps.length > 0, 'Updated dependencies are missing');
  return deps.map(dep => {
    assert(typeof dep['dependency-name'] === 'string' && dep['dependency-name'].length > 0);
    return { name: dep['dependency-name'], type: dep['update-type'] || 'unknown' };
  });
}

export function automaticUpdate(deps, exceptions = []) {
  return deps.every(dep => MINOR_PATCH.includes(dep.type)
    || (dep.type === 'version-update:semver-major' && exceptions.includes(dep.name)));
}

function same(a, b, message) {
  assert(isDeepStrictEqual(a, b), message);
}

function dependencyMap(value) {
  const copy = structuredClone(value);
  for (const key of ['dependencies', 'devDependencies', 'peerDependencies', 'optionalDependencies', 'overrides', 'resolutions']) delete copy[key];
  return copy;
}

function productPackages(lock, config) {
  const packages = lock.packages || {};
  const visited = new Set();
  function resolve(from, name) {
    let directory = from;
    for (;;) {
      const candidate = path.posix.join(directory, 'node_modules', name);
      if (packages[candidate]) return packages[candidate].link ? packages[candidate].resolved : candidate;
      if (!directory || directory === '.') return null;
      directory = path.posix.dirname(directory);
    }
  }
  function visit(key) {
    if (!key || visited.has(key)) return;
    visited.add(key);
    const entry = packages[key];
    for (const name of Object.keys({ ...entry.dependencies, ...entry.optionalDependencies, ...entry.peerDependencies })) visit(resolve(key, name));
  }
  for (const [key, entry] of Object.entries(packages)) {
    if (key.includes('node_modules')) continue;
    const manifest = key ? `${key}/package.json` : 'package.json';
    for (const name of Object.keys({ ...entry.dependencies, ...entry.devDependencies, ...entry.optionalDependencies, ...entry.peerDependencies })) {
      if (!config.noRelease?.some(exclusion => exclusion.name === name && exclusion.files.includes(manifest) && exclusion.reason)) visit(resolve(key, name));
    }
  }
  return visited;
}

function goDefinition(content) {
  return content.split('\n').map(line => line
    .replace(/^(\s*(?:require\s+)?[^\s]+\s+)v\d[^\s]*(\s*(?:\/\/.*)?)$/, '$1<VERSION>$2')
    .replace(/^(go|toolchain)\s+go?\d[^\s]*$/, '$1 <VERSION>')
    .replace(/^go\s+\d[^\s]*$/, 'go <VERSION>')).join('\n');
}

function stripActionReferences(value, allowlist, updates) {
  if (!value || typeof value !== 'object') return;
  if (typeof value.uses === 'string') {
    const match = value.uses.match(/^([^@]+)@([0-9a-f]{40})$/);
    if (match) {
      assert(allowlist.includes(match[1]), `Action is not allowlisted: ${match[1]}`);
      updates.add(match[1]);
      value.uses = `${match[1]}@<REFERENCE>`;
    } else {
      assert(!value.uses.includes('@') || value.uses.startsWith('./'), 'External Actions must use a full commit SHA');
    }
  }
  for (const child of Object.values(value)) stripActionReferences(child, allowlist, updates);
}

function dockerDefinition(content, images, refs) {
  return content.split('\n').map(line => {
    const match = line.match(/^(\s*(?:FROM\s+(?:--platform=\S+\s+)?|COPY\s+--from=))(\S+)(.*)$/i);
    if (!match) return line;
    const image = match[2];
    const name = image.replace(/@sha256:[a-f0-9]{64}$/, '').replace(/:[^/:]+$/, '');
    if (!images.includes(name)) return line;
    assert(!image.includes('$'), 'Dynamic image references cannot be automatically updated');
    refs.add(name);
    return `${match[1]}${name}@<REFERENCE>${match[3]}`;
  }).join('\n');
}

export function validateFile(path, before, after, config) {
  assert(before !== null && after !== null, `File creation, deletion, and renames require review: ${path}`);
  if (/^\.github\/workflows\/[^/]+\.ya?ml$/.test(path)) {
    const oldDoc = parse(before, { uniqueKeys: true });
    const newDoc = parse(after, { uniqueKeys: true });
    const refs = new Set();
    stripActionReferences(oldDoc, config.actions, refs);
    stripActionReferences(newDoc, config.actions, refs);
    same(oldDoc, newDoc, `Only existing Action references may change: ${path}`);
    assert(refs.size > 0, 'No allowlisted Action references found');
    return { names: [...refs], manifest: path, kind: 'actions' };
  }
  assert(config.files.includes(path), `File is not allowlisted: ${path}`);
  if (/(^|\/)package\.json$/.test(path)) {
    const a = JSON.parse(before), b = JSON.parse(after);
    same(dependencyMap(a), dependencyMap(b), `Only dependency fields may change: ${path}`);
    const names = new Set();
    for (const key of ['dependencies', 'devDependencies', 'peerDependencies', 'optionalDependencies', 'overrides', 'resolutions']) {
      for (const name of new Set([...Object.keys(a[key] || {}), ...Object.keys(b[key] || {})])) {
        if (!isDeepStrictEqual(a[key]?.[name], b[key]?.[name])) names.add(name);
      }
    }
    return { names: [...names], manifest: path, kind: 'npm' };
  }
  if (/(^|\/)package-lock\.json$/.test(path)) {
    const a = JSON.parse(before), b = JSON.parse(after);
    assert(a.lockfileVersion === b.lockfileVersion && a.name === b.name && a.version === b.version, 'Lockfile metadata changed');
    for (const [name, value] of Object.entries(a.packages || {})) {
      if (name.includes('node_modules')) continue;
      same(dependencyMap(value), dependencyMap(b.packages?.[name]), 'Workspace lockfile metadata changed');
    }
    const product = new Set([...productPackages(a, config), ...productPackages(b, config)]);
    const runtime = [...product].some(key => !isDeepStrictEqual(a.packages?.[key], b.packages?.[key]));
    return { names: [], manifest: path, kind: 'lock', runtime };
  }
  if (path === 'go.mod') {
    same(goDefinition(before), goDefinition(after), 'Only existing Go dependency and toolchain versions may change');
    return { names: [], manifest: path, kind: 'go' };
  }
  if (path === 'go.sum') {
    assert(after.split('\n').every(line => !line || /^\S+ v\S+ h1:[A-Za-z0-9+/]+=*$/.test(line)), 'Invalid go.sum');
    return { names: [], manifest: path, kind: 'go' };
  }
  if (path.endsWith('.csproj') || path === 'Directory.Packages.props') {
    assert(!/<!DOCTYPE|<!ENTITY/i.test(before + after), 'XML entities require review');
    const normalize = text => text.replace(/(<Package(?:Reference|Version)\b[^>]*\b(?:Include|Update)="([^"]+)"[^>]*\bVersion=")[^"]+("[^>]*\/?\s*>)/g,
      '$1<VERSION>$3');
    same(normalize(before), normalize(after), 'Only PackageReference/PackageVersion Version attributes may change');
    return { names: [], manifest: path, kind: 'nuget' };
  }
  if (path === 'global.json') {
    const a = JSON.parse(before), b = JSON.parse(after);
    assert(a.sdk?.version && b.sdk?.version);
    a.sdk.version = b.sdk.version;
    same(a, b, 'Only .NET SDK version may change');
    return { names: ['dotnet-sdk'], manifest: path, kind: 'toolchain' };
  }
  if (/Dockerfile/.test(path)) {
    const refs = new Set();
    same(dockerDefinition(before, config.images || [], refs), dockerDefinition(after, config.images || [], refs), 'Only allowlisted image references may change');
    assert(refs.size > 0, 'No allowlisted image references found');
    return { names: [...refs], manifest: path, kind: 'docker' };
  }
  throw new Error(`Unsupported dependency definition: ${path}`);
}

export function releaseImpact(deps, changes, exclusions = []) {
  if (!changes.length || !deps.length) return true;
  if (changes.some(change => change.runtime)) return true;
  return deps.some(dep => !exclusions.some(exclusion => exclusion.name === dep.name && exclusion.reason
    && changes.filter(change => change.kind !== 'lock').every(change => exclusion.files.includes(change.manifest))));
}

export function squash(deps, impact, number, sha, breaking = '') {
  const title = `${impact ? 'fix' : 'chore'}(deps): update ${deps.length === 1 ? deps[0].name : `${deps.length} dependencies`}`;
  const body = [`Dependency update #${number}`, `Validated head: ${sha}`, '', ...deps.map(dep => `- ${dep.name}: ${dep.type}`)];
  if (breaking) body.push('', `BREAKING CHANGE: ${breaking}`);
  return { title, body: body.join('\n') };
}

export function assetNames(config, version) {
  assert(semver.valid(version), 'Invalid release version');
  return config.assets.map(name => name.replaceAll('{version}', version));
}

export function validateChecksums(text, files) {
  const entries = text.trim().split(/\r?\n/).map(line => {
    const match = line.match(/^([a-f0-9]{64})\s+\*?([^/\\]+)$/i);
    assert(match, 'Malformed SHA-256 checksums');
    return [match[2], match[1].toLowerCase()];
  });
  assert(new Set(entries.map(([name]) => name)).size === entries.length, 'Duplicate checksums');
  for (const name of files) assert(entries.some(([file]) => file === name), `Missing checksum: ${name}`);
  return new Map(entries);
}
