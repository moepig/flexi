import assert from 'node:assert/strict';
import { appendFileSync } from 'node:fs';

export class GitHub {
  constructor(token, repository, fetcher = fetch) {
    assert(token && /^[\w.-]+\/[\w.-]+$/.test(repository), 'Repository token and repository are required');
    this.token = token;
    this.repository = repository;
    this.fetcher = fetcher;
    this.root = `/repos/${repository}`;
  }
  async request(method, path, body) {
    const response = await this.fetcher(`https://api.github.com${path}`, {
      method,
      headers: { authorization: `Bearer ${this.token}`, accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28', 'content-type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    if (!response.ok) {
      const error = new Error(`${method} ${path}: HTTP ${response.status}`);
      error.status = response.status;
      throw error;
    }
    return response.status === 204 ? null : response.json();
  }
  async get(path) { return this.request('GET', `${this.root}${path}`); }
  async optional(path) {
    try { return await this.get(path); } catch (error) { if (error.status === 404) return null; throw error; }
  }
  async pages(path, key) {
    const values = [];
    for (let page = 1; ; page++) {
      const result = await this.get(`${path}${path.includes('?') ? '&' : '?'}per_page=100&page=${page}`);
      const items = key ? result[key] : result;
      assert(Array.isArray(items), `Invalid paginated response: ${path}`);
      values.push(...items);
      if (items.length < 100) return values;
    }
  }
  async content(path, ref) {
    const result = await this.get(`/contents/${path}?ref=${encodeURIComponent(ref)}`);
    assert(result.type === 'file' && result.encoding === 'base64', `Unsupported file: ${path}`);
    return Buffer.from(result.content, 'base64').toString('utf8');
  }
  async mutate(method, path, body) { return this.request(method, `${this.root}${path}`, body); }
  async graphql(query, variables) {
    const result = await this.request('POST', '/graphql', { query, variables });
    assert(!result.errors, `GraphQL request failed: ${JSON.stringify(result.errors)}`);
    return result.data;
  }
  async check(sha, name, conclusion, record) {
    const output = { title: `${name}: ${conclusion}`, summary: `Validated head: ${sha}`, text: JSON.stringify(record, null, 2) };
    assert(output.text.length < 65000, 'Decision exceeds Check Run size limit');
    return this.mutate('POST', '/check-runs', { name, head_sha: sha, status: 'completed', conclusion, output });
  }
}

export function output(name, value) {
  if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `${name}=${String(value)}\n`);
}

export function summary(message) {
  console.log(message);
  if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, `${message}\n`);
}
