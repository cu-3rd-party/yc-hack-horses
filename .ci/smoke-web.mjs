// Runs against the finished production image with an empty, disposable journal.
import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';

const base = process.env.JOURNAL_URL || 'http://127.0.0.1:8080/';
async function get(path, type, status = 200) {
  const response = await fetch(new URL(path, base), { signal: AbortSignal.timeout(5000) });
  assert.equal(response.status, status, path);
  assert.match(response.headers.get('content-type') || '', type, path);
  return response;
}

for (let attempt = 0; ; attempt++) {
  try {
    await get('/readyz', /application\/json/);
    break;
  } catch (error) {
    if (attempt >= 29) throw error;
    await delay(1000);
  }
}

const html = await (await get('/', /text\/html/)).text();
assert.match(html, /<title>ЛошID/);
for (const [pattern, mime] of [[/src="([^"]+\.js)"/, /javascript/], [/href="([^"]+\.css)"/, /text\/css/]]) {
  const asset = html.match(pattern)?.[1];
  assert.ok(asset, 'Expected a built asset: ' + pattern);
  await get(asset, mime);
}
await get('/photos/single.jpg', /image\/jpeg/);
await get('/health', /application\/json/);
await get('/api/scalar', /text\/html/);
assert.ok((await (await get('/api/v1/node', /application\/json/)).json()).node_id);
await get('/api/v1/unknown', /application\/json/, 404);
console.log('Web renderer, assets, node API, docs and probes passed.');
