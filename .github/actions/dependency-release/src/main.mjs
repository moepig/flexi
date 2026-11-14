import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { GitHub } from './github.mjs';
import { dependencies } from './dependencies.mjs';
import { discover, finalize, prepare, ready, state, verifyDistribution } from './releases.mjs';
import { containers } from './containers.mjs';

async function main() {
  const input = name => process.env[`INPUT_${name.toUpperCase().replaceAll('-', '_')}`] || process.env[`INPUT_${name.toUpperCase()}`] || '';
  const api = new GitHub(input('token'), process.env.GITHUB_REPOSITORY);
  const config = JSON.parse(await api.content('.github/dependency-release.json', 'main'));
  assert(config.schemaVersion === 1 && /^[0-9a-f]{40}$/.test(config.bootstrap), 'Invalid trusted main configuration');
  const event = JSON.parse(readFileSync(process.env.GITHUB_EVENT_PATH, 'utf8'));
  switch (input('mode')) {
    case 'dependencies': await dependencies(api, config, event); break;
    case 'prepare': await prepare(api, config, event, input('operation') || 'reconcile', input('initial-version')); break;
    case 'discover': await discover(api, config); break;
    case 'finalize': await finalize(api, config, input('release-id'), input('tag'), input('sha')); break;
    case 'containers': await containers(api, config, input('release-id'), input('tag'), input('sha')); break;
    case 'verify': await verifyDistribution(api, config, input('release-id'), input('tag'), input('sha')); break;
    case 'ready': await ready(api, config, await state(api)); break;
    default: throw new Error('Unknown control mode');
  }
}
main().catch(error => { console.error(error.stack); process.exitCode = 1; });
