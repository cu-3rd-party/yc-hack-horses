// Exercise a packaged desktop against an isolated Go node. Only the model is a fixture.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import { checkPassports } from './passports.mjs';
import { chromium, _electron as electron } from 'playwright-core';

const project = fileURLToPath(new URL('../', import.meta.url));
const nodeBinary = process.env.JOURNAL_NODE_BINARY;
assert.ok(
  nodeBinary,
  'JOURNAL_NODE_BINARY must point to the Go node executable',
);
const root = await mkdtemp(path.join(tmpdir(), 'loshid-go-review-'));
const sample = path.join(project, 'public/photos/pair.jpg');
const bytes = await readFile(sample);
const emptyFixture = await readFile(path.join(project, 'public/photos/single.jpg'));
let analyses = 0;
const model = createServer(async (req, res) => {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  const body = Buffer.concat(chunks);
  if (req.method === 'POST' && body.equals(emptyFixture)) {
    res.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify({
      width: 1440, height: 960, model_version: 'desktop-test', detections: [],
    }));
    return;
  }
  if (req.method !== 'POST' || !body.equals(bytes)) {
    res.writeHead(400).end();
    return;
  }
  analyses++;
  const detection = (x, embedding) => ({
    detection_confidence: 0.97,
    horse_polygon: [
      [x, 192],
      [x + 288, 230],
      [x + 260, 883],
      [x + 29, 768],
    ],
    traits: [
      {
        key: 'muzzle',
        value: 'light',
        polygon: [
          [x, 192],
          [x + 29, 211],
          [x + 7, 250],
        ],
      },
    ],
    embedding,
  });
  res.writeHead(200, { 'content-type': 'application/json' }).end(
    JSON.stringify({
      model_version: 'desktop-test',
      width: 1440,
      height: 960,
      detections: [
        detection(72, analyses === 1 ? [1, 0] : [4, 3]),
        detection(792, [0, 1]),
      ],
    }),
  );
});
const listen = async (server) => {
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  return '127.0.0.1:' + server.address().port;
};
let app, node, stopped;
try {
  const modelAddress = await listen(model);
  const reservation = createServer();
  const address = await listen(reservation);
  await new Promise((resolve) => reservation.close(resolve));
  const origin = 'http://' + address;
  node = spawn(nodeBinary, ['web'], {
    cwd: root,
    env: {
      ...process.env,
      NODE_ID: '01990000-0000-7000-8000-00000000000a',
      WEB_DIR: path.join(project, 'dist'),
      DATA_DIR: root,
      STORAGE_DRIVER: 'sqlite',
      NODE_DSN: path.join(root, 'journal.db'),
      LISTEN_ADDR: address,
      GRPC_LISTEN_ADDR: '127.0.0.1:0',
      MDNS_ENABLED: 'false',
      SYNC_ENABLED: 'false',
      INFER_COMPARISON_URL: 'http://' + modelAddress,
      MIGRATIONS_DIR: path.resolve(project, '../backend/migrations'),
    },
    stdio: ['ignore', 'ignore', 'inherit'],
  });
  stopped = once(node, 'exit');
  let ready = false;
  for (let n = 0; n < 100 && node.exitCode === null; n++) {
    if (
      await fetch(origin + '/readyz').then(
        (r) => r.ok,
        () => false,
      )
    ) {
      ready = true;
      break;
    }
    await delay(100);
  }
  assert.ok(ready, 'Go node did not become ready');

  const binary = process.env.JOURNAL_BINARY;
  app = await electron.launch({
    executablePath: binary || undefined,
    args: [
      ...(binary ? [] : [project]),
      '--user-data-dir=' + path.join(root, 'profile'),
    ],
    cwd: project,
  });
  await app.evaluate(({ net }, target) => {
    const original = net.fetch.bind(net);
    net.fetch = (url, options) =>
      original(
        typeof url === 'string' && url.startsWith('http://127.0.0.1:8080/')
          ? target + new URL(url).pathname + new URL(url).search
          : url,
        options,
      );
  }, origin);
  const page = await app.firstWindow();
  page.setDefaultTimeout(8000);
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.reload();
  const button = (name) => page.getByRole('button', { name, exact: true });
  const heading = (name) => page.getByRole('heading', { name, exact: true });
  const dialog = page.getByRole('dialog');
  const confirm = () =>
    dialog.getByRole('button', { name: 'Подтвердить' }).click();
  const input = page.getByLabel('Файл наблюдения');
  await heading('Новое наблюдение').waitFor();
  const files = ['Первое.jpg', 'Второе.jpg', 'Третье.jpg'].map(name => ({
    name, mimeType: 'image/jpeg', buffer: bytes,
  }));
  await input.setInputFiles([
    { name: 'Повреждённый.jpg', mimeType: 'image/jpeg', buffer: Buffer.from('broken') },
    { name: 'Пустое поле.jpg', mimeType: 'image/jpeg', buffer: emptyFixture },
    ...files,
  ]);
  await button('Повторить').click();
  await button('Пропустить снимок').click();
  await page.getByText('Снимок 3 из 5 · Первое.jpg').waitFor();
  for (const [i, name] of ['Сарана', 'Буран'].entries()) {
    await heading('Новое наблюдение ' + (i + 1)).waitFor();
    await page.getByText('Подходящих особей не найдено.').waitFor();
    await button('Нет подходящих').click();
    await dialog.getByLabel('Имя лошади').fill(name);
    await dialog.getByLabel('Комментарий').fill('Черты ' + name);
    await dialog.getByLabel('Название наблюдения').fill('Эталон ' + name);
    await confirm();
  }
  await page.getByText('Снимок 4 из 5 · Второе.jpg').waitFor();
  await heading('Эталон Сарана').waitFor();
  await page.getByRole('button', { name: /Сарана.*80%/ }).waitFor();
  await button('Лошадь — вперёд').click();
  await heading('Эталон Буран').waitFor();
  await page.getByRole('button', { name: /Буран.*100%/ }).waitFor();
  await button('Лошадь — назад').click();
  await button('Зафиксировать результат').click();
  await dialog.getByLabel('Название наблюдения').fill('Повторная встреча');
  await dialog.getByLabel('Комментарий').fill('Повторная встреча у реки');
  await confirm();
  await heading('Новое наблюдение 2').waitFor();
  await button('Отменить сравнение').click();
  await confirm();
  await heading('Новое наблюдение').waitFor();
  const response = await fetch(
    origin + '/api/v1/observations?include=photos,annotations',
  );
  assert.equal(response.status, 200);
  const history = await response.json();
  assert.equal(history.length, 3);
  assert.deepEqual(history.map(o => o.comment), ['Черты Сарана', 'Черты Буран', 'Повторная встреча у реки']);
  const masks = history.map((o) => o.photos[0].annotation);
  assert.deepEqual(
    masks.map((a) => a.horse_polygon[0]),
    [
      [0.05, 0.2],
      [0.55, 0.2],
      [0.05, 0.2],
    ],
  );
  assert.deepEqual(masks[0].traits[0].polygon[0], [0.05, 0.2]);
  assert.equal(analyses, 2);
  await app.evaluate(({ session }) => {
    // Packaged passports must open with only the local node available.
    session.defaultSession.webRequest.onBeforeRequest(
      { urls: ['https://*/*', 'http://*/*'] },
      ({ url }, callback) => callback({ cancel: new URL(url).hostname !== '127.0.0.1' }),
    );
  });
  await checkPassports(page, origin, 'Electron');
  if (process.argv.includes('--web')) {
    const browser = await chromium.launch({ channel: process.env.JOURNAL_BROWSER });
    try {
      const web = await browser.newPage();
      web.on('pageerror', error => errors.push(error.message));
      await web.goto(origin);
      await checkPassports(web, origin, 'Web');
    } finally {
      await browser.close();
    }
  }
  assert.deepEqual(errors, []);
  console.log(
    'Electron + Go + SQLite: upload, two independent rankings, annotated history, match and cancellation passed.',
  );
} finally {
  await app?.close();
  if (node && node.exitCode === null && node.kill('SIGTERM')) await stopped;
  model.closeAllConnections();
  await new Promise((resolve) => model.close(resolve));
  await rm(root, { recursive: true, force: true });
}
