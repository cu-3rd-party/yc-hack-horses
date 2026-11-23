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

// Каждый собранный ассет страницы, а не только первый (PR-ревью #35):
// matchAll ловит дополнительные чанки/файлы css — их 404 раньше проходил мимо.
async function checkPageAssets(pageHtml, what) {
  for (const [pattern, mime] of [[/src="([^"]+\.js)"/g, /javascript/], [/href="([^"]+\.css)"/g, /text\/css/]]) {
    const assets = [...pageHtml.matchAll(pattern)].map((match) => match[1]);
    assert.ok(assets.length, 'Expected a built asset on ' + what + ': ' + pattern);
    for (const asset of assets) await get(asset, mime);
  }
}

const html = await (await get('/', /text\/html/)).text();
assert.match(html, /<title>ЛошID/);
// MPA (ADR-0015): остальные страницы — отдельные html-входы.
for (const page of ['index.html', 'gallery.html', 'observations.html', 'system.html']) {
  const pageHtml = await (await get('/' + page, /text\/html/)).text();
  assert.match(pageHtml, /<title>ЛошID/, page);
  await checkPageAssets(pageHtml, page);
}
await checkPageAssets(html, '/');
await get('/pdfjs/wasm/openjpeg.wasm', /application\/wasm/);
await get('/photos/single.jpg', /image\/jpeg/);
await get('/health', /application\/json/);
await get('/api/scalar', /text\/html/);
assert.ok((await (await get('/api/v1/node', /application\/json/)).json()).node_id);
await get('/api/v1/unknown', /application\/json/, 404);
console.log('Web renderer, assets, node API, docs and probes passed.');
