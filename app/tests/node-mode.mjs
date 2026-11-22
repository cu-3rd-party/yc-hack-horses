// Run against the built renderer. JOURNAL_TEST_NODE may point to a disposable, empty Go node.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { _electron as electron } from 'playwright-core';

const project = fileURLToPath(new URL('../', import.meta.url));
const profile = await mkdtemp(path.join(tmpdir(), 'loshid-node-test-'));
const sample = path.join(project, 'public/photos/pair.jpg');
const bytes = await readFile(sample);
const hash = createHash('sha256').update(bytes).digest('hex');
const annotation = {
  horse_polygon: [
    [0.1, 0.2],
    [0.4, 0.2],
    [0.4, 0.8],
  ],
  traits: [
    {
      key: 'muzzle',
      value: 'light',
      polygon: [
        [0.1, 0.2],
        [0.12, 0.2],
        [0.11, 0.25],
      ],
    },
    { key: 'scars', value: 'none', polygon: null },
  ],
};
const horses = [
  { id: 'h1', metadata: { name: 'Кедр' } },
  { id: 'h2', metadata: { name: 'Ветер' } },
];
const observation = (id, horse) => ({
  id,
  horse_id: horse,
  observer_id: 'tester',
  location: '',
  observed_at: '2026-11-20T12:00:00Z',
  blob_ids: [hash],
});
const history = horses.map(({ id }, i) => ({
  ...observation('obs' + i, id),
  photos: [1, 2].map((n) => ({
    photo_id: id + 'p' + n,
    blob_sha256: hash,
    name: id + '-photo-' + n,
    annotation,
  })),
}));
const calls = [];
let online = false,
  scenario = 'pair';
const fixture = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  const route = url.pathname.slice('/api/v1'.length);
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  const raw = Buffer.concat(chunks);
  const body = req.headers['content-type']?.includes('application/json')
    ? JSON.parse(raw.toString())
    : undefined;
  calls.push({ method: req.method, route, body, query: url.search });
  function json(data, status = 200) {
    res.writeHead(status, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(data));
  }
  if (!online) return json({ message: 'offline' }, 503);
  if (route === '/node')
    return json({
      node_id: 'test',
      driver: 'sqlite',
      app_version: 'test',
      role_hint: 'local',
      sync: [],
      event_count: 0,
    });
  if (route === '/horses') return json(horses);
  if (route === '/observations')
    return json(
      url.searchParams.has('horse_id')
        ? history.filter((o) => o.horse_id === url.searchParams.get('horse_id'))
        : history,
    );
  if (route === '/blobs' && req.method === 'GET')
    return json([{ id: 'blob', sha256: hash, size: bytes.length }]);
  if (route === '/blobs' && req.method === 'POST') {
    if (!raw.includes(bytes) || !raw.includes('name="file"'))
      return json({ message: 'invalid multipart' }, 400);
    return json({ sha256: hash }, 201);
  }
  if (route === '/blobs/' + hash) {
    res.writeHead(200, { 'Content-Type': 'image/jpeg' });
    return res.end(bytes);
  }
  if (route === '/comparisons') {
    const detections = horses.map(({ id, metadata }, i) => ({
      detection_id: 'det' + i,
      detection_confidence: 0.98,
      annotation,
      candidates:
        scenario === 'no-candidates'
          ? []
          : [
              {
                horse_id: id,
                name: metadata.name,
                cosine_similarity: 0.84,
                best_photo_id: id + 'p2',
              },
            ],
    }));
    return json(
      {
        comparison_id: 'cmp',
        blob_sha256: hash,
        detections: scenario === 'empty' ? [] : detections,
      },
      201,
    );
  }
  if (route.endsWith('/decision'))
    return json({ horse_id: 'h1', observation_id: 'new', remaining_detection_ids: ['det1'] });
  if (route.endsWith('/cancel'))
    return json({
      status: 'cancelled',
      saved_observation_ids: ['new'],
      skipped_detection_ids: ['det1'],
    });
  return json({ message: 'unknown route' }, 404);
});
await new Promise((resolve) => fixture.listen(0, '127.0.0.1', resolve));
const fixtureURL = 'http://127.0.0.1:' + fixture.address().port;
const binary = process.env.JOURNAL_BINARY;
let app, page;
const errors = [];
async function launch(target) {
  app = await electron.launch({
    executablePath: binary || undefined,
    args: [...(binary ? [] : [project]), '--user-data-dir=' + profile],
    cwd: project,
  });
  // Redirect only the test process's outgoing loopback requests to the isolated fixture.
  await app.evaluate(({ net }, upstream) => {
    const original = net.fetch.bind(net);
    net.fetch = (url, options) =>
      original(
        typeof url === 'string' && url.startsWith('http://127.0.0.1:8080/')
          ? upstream + url.slice('http://127.0.0.1:8080'.length)
          : url,
        options,
      );
  }, target);
  page = await app.firstWindow();
  page.setDefaultTimeout(8000);
  page.on('pageerror', (error) => errors.push(error.message));
  await page.reload();
}
const nav = (label) => page.getByRole('navigation').getByRole('button', { name: label }).click();
const button = (label) => page.getByRole('button', { name: label, exact: true });
const heading = (name) => page.getByRole('heading', { name, exact: true });
const mode = () => page.getByRole('switch', { name: 'Демосценарии', exact: true });
const upload = () => page.getByLabel('Файл наблюдения').setInputFiles(sample);
const dlg = () => page.getByRole('dialog');

try {
  await launch(fixtureURL);
  await page.getByText('Локальная нода недоступна').waitFor();
  await nav('Настройки');
  assert.equal(await mode().getAttribute('aria-checked'), 'false');
  await mode().click();
  await nav('Распознавание');
  await button('Табун у сопки Две лошади').waitFor();
  await button('Табун у сопки Две лошади').click();
  await heading('Сравнение наблюдений').waitFor();
  await nav('Настройки');
  await app.close();
  await launch(fixtureURL);
  await heading('Сравнение наблюдений').waitFor();
  await nav('Настройки');
  online = true;
  await mode().click();
  await nav('Распознавание');
  if (await button('Повторить').isVisible()) await button('Повторить').click();
  await heading('Новое наблюдение').waitFor();
  assert.equal(await heading('Примеры для распознавания').count(), 0);

  await upload();
  await heading('Сравнение наблюдений').waitFor();
  await heading('h1-photo-2').waitFor();
  await button('Фото — назад').click();
  await heading('h1-photo-1').waitFor();
  await button('Лошадь — вперёд').click();
  await heading('h2-photo-2').waitFor();
  await button('Лошадь — назад').click();
  await heading('h1-photo-1').waitFor();
  await button('Зафиксировать результат').click();
  await dlg().getByRole('button', { name: 'Подтвердить' }).click();
  await heading('Новое наблюдение 2').waitFor();
  await button('Отменить сравнение').click();
  await dlg().getByRole('button', { name: 'Подтвердить' }).click();
  await heading('Новое наблюдение').waitFor();
  assert.equal(calls.filter((c) => c.route === '/comparisons').length, 1);
  assert.deepEqual(calls.find((c) => c.route === '/comparisons').body, {
    blob_sha256: hash,
    top_k: 5,
  });
  assert.ok(
    calls.some(
      (c) =>
        c.method === 'PUT' &&
        c.route === '/comparisons/cmp/detections/det0/decision' &&
        c.body.action === 'match' &&
        c.body.horse_id === 'h1',
    ),
  );
  assert.ok(calls.some((c) => c.route === '/comparisons/cmp/cancel'));
  for (const horse of horses)
    assert.ok(calls.some((c) => new URLSearchParams(c.query).get('horse_id') === horse.id));

  scenario = 'empty';
  await upload();
  await page.getByText('На снимке не обнаружено лошадей.').waitFor();
  scenario = 'no-candidates';
  await upload();
  await page.getByText('Подходящих особей не найдено.').waitFor();
  assert.ok(await button('Зафиксировать результат').isDisabled());
  await button('Нет подходящих').click();
  await dlg().getByLabel('Имя лошади').fill('Искра');
  await dlg().getByRole('button', { name: 'Подтвердить' }).click();
  await heading('Новое наблюдение 2').waitFor();
  assert.ok(calls.some((c) => c.body?.action === 'create_horse' && c.body.horse_name === 'Искра'));

  await nav('Настройки');
  await mode().click();
  await nav('Распознавание');
  await page.getByText('Лошадь 1/2', { exact: true }).waitFor();
  await page.getByRole('button', { name: /Сарана.*96%/ }).waitFor();
  await nav('Настройки');
  await mode().click();
  await nav('Распознавание');
  await heading('Новое наблюдение 2').waitFor();

  if (process.env.JOURNAL_TEST_NODE) {
    await app.close();
    const target = new URL(process.env.JOURNAL_TEST_NODE);
    assert.equal(target.hostname, '127.0.0.1');
    const api = target.origin + '/api/v1';
    const read = async (route) => (await fetch(api + route)).json();
    assert.deepEqual(await read('/observations'), [], 'Use a fresh, disposable test node');
    assert.deepEqual(await read('/horses'), []);
    await launch(target.origin);
    await nav('Особи');
    await page.getByText('0 в каталоге').waitFor();
    const horse = await (
      await fetch(api + '/horses', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ metadata: { name: 'Живая нода' } }),
      })
    ).json();
    await page.reload();
    await nav('Наблюдения');
    await button('Ручная запись').click();
    await page.getByLabel('Наблюдатель').fill('tester');
    await page.getByLabel('Место наблюдения').fill('У ручья');
    await page.getByRole('combobox', { name: 'Особь' }).click();
    await page.getByRole('option', { name: 'Живая нода' }).click();
    await page.getByLabel('Фотографии').setInputFiles(sample);
    await button('Сохранить запись').click();
    await heading('Журнал наблюдений').waitFor();
    const rows = await read('/observations');
    assert.equal(rows.length, 1);
    assert.deepEqual(
      [rows[0].horse_id, rows[0].observer_id, rows[0].location, rows[0].blob_ids],
      [horse.id, 'tester', 'У ручья', [hash]],
    );
    const result = Buffer.from(await (await fetch(api + '/blobs/' + hash)).arrayBuffer());
    assert.ok(bytes.equals(result));
    await page.locator('.photo-card img').first().waitFor();
    await page.waitForFunction(() => document.querySelector('.photo-card img')?.naturalWidth > 0);
    await nav('Распознавание');
    await upload();
    await page.getByText('Сравнение недоступно на этой ноде.').waitFor();
    await nav('Особи');
    await page.getByRole('button', { name: /Живая нода/ }).click();
    await page.locator('.photo-card img').waitFor();
    console.log(
      'Verified empty and populated journal, manual observation, original bytes and unsupported comparisons on Go master',
    );
  }
  assert.deepEqual(errors, []);
  console.log(
    'Verified mode persistence, HTTP comparison, independent rankings, best reference photo, decisions, cancellation and empty results',
  );
} catch (error) {
  console.error(
    'Last requests',
    calls.slice(-10),
    'Renderer',
    await page
      ?.locator('main')
      .innerText()
      .catch(() => ''),
    errors,
  );
  throw error;
} finally {
  if (app) await app.close().catch(() => {});
  fixture.closeAllConnections();
  await new Promise((resolve) => fixture.close(resolve));
  await rm(profile, { recursive: true, force: true });
}
