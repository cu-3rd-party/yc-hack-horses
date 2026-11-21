import assert from 'node:assert/strict';
import { mkdtemp, mkdir, rm, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import { _electron as electron } from 'playwright-core';
const project = fileURLToPath(new URL('../', import.meta.url));
const profile = await mkdtemp(path.join(tmpdir(), 'takhi-smoke-'));
const screenshots = process.env.JOURNAL_SCREENSHOTS;
let app, page;
const errors = [],
  network = [];
const external = (url) => /^https?:|^wss?:/.test(url);
async function launch() {
  const binary = process.env.JOURNAL_BINARY;
  app = await electron.launch({
    executablePath: binary || undefined,
    args: [...(binary ? [] : [project]), '--user-data-dir=' + profile],
    cwd: project,
    timeout: 30000,
  });
  page = await app.firstWindow();
  page.setDefaultTimeout(8000);
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('request', (request) => {
    if (external(request.url())) network.push(request.url());
  });
  page.on('websocket', (socket) => network.push(socket.url()));
  await page.getByRole('heading', { name: 'Новое наблюдение', exact: true }).waitFor();
  await page.evaluate(() => document.fonts.ready);
}
const button = (name, options = {}) => page.getByRole('button', { name, exact: true, ...options });
const nav = (name) =>
  page.getByRole('navigation').getByRole('button', { name, exact: false }).click();
const dialog = () => page.getByRole('dialog');
async function snap(name) {
  if (screenshots) {
    await mkdir(screenshots, { recursive: true });
    await page.waitForFunction(() =>
      Array.from(document.images).every((img) => img.complete && img.naturalWidth > 0),
    );
    await page.mouse.move(0, 0);
    await page.screenshot({ path: path.join(screenshots, name + '.png'), animations: 'disabled' });
  }
}
async function match(name, creating = false) {
  await button(creating ? 'Нет подходящих' : 'Зафиксировать результат').click();
  if (name) await dialog().getByLabel('Название наблюдения').fill(name);
  await dialog().getByRole('button', { name: 'Подтвердить' }).click();
  await dialog().waitFor({ state: 'hidden' });
}
try {
  await launch();
  await snap('home');
  await page.locator('.recent').first().click();
  await dialog().waitFor();
  await dialog().getByRole('button', { name: 'Открыть фото на весь экран' }).click();
  await page.locator('.dialog.fullscreen').waitFor();
  await page.keyboard.press('Escape');
  await dialog().getByRole('button', { name: 'Открыть фото на весь экран' }).waitFor();
  await page.keyboard.press('Escape');
  await nav('Распознавание');
  await nav('Настройки');
  await page.getByRole('radio', { name: 'Синяя' }).check();
  assert.equal(await page.locator('html').getAttribute('data-scheme'), 'blue');
  await page.getByRole('radio', { name: 'Тёмная' }).check();
  await page.reload();
  await page.getByRole('heading', { name: 'Новое наблюдение', exact: true }).waitFor();
  assert.equal(await page.locator('html').getAttribute('data-scheme'), 'dark');
  await snap('home-dark');
  await nav('Настройки');
  await page.getByRole('radio', { name: 'Светлая' }).check();
  await page.getByRole('switch', { name: 'Сбой один раз' }).click();
  await page.waitForFunction(
    () => document.querySelector('[role=switch]')?.getAttribute('aria-checked') === 'true',
  );
  await nav('Распознавание');
  await page.getByRole('button', { name: /Табун у сопки/ }).click();
  await page.getByRole('button', { name: /Анализируем снимок/ }).waitFor();
  assert.equal(await page.locator('.dropzone').isDisabled(), true);
  assert.ok(await page.locator('.file-state').innerText());
  await page.getByRole('heading', { name: 'Сравнение наблюдений' }).waitFor();
  await page.getByText('Лошадь 1/2', { exact: true }).waitFor();
  const leftPhoto = page.locator('.compare-card').first().locator('.marked img');
  const originalSrc = await leftPhoto.getAttribute('src');
  await button('Лошадь — вперёд').click();
  await page.getByText('Лошадь 2/2', { exact: true }).waitFor();
  assert.equal(await leftPhoto.getAttribute('src'), originalSrc);
  await page.getByRole('heading', { name: 'Новое наблюдение 2' }).waitFor();
  await button('Лошадь — назад').click();
  const toggle = page.getByRole('switch', { name: 'Показать трейты' });
  await toggle.click();
  assert.equal(await toggle.getAttribute('aria-checked'), 'false');
  await toggle.click();
  await page.getByRole('button', { name: /Алтай.*83%/ }).click();
  await button('Фото — вперёд').click();
  await page.getByText('Фото 2/2', { exact: true }).waitFor();
  await button('Выбрать фотографию кандидата').click();
  await dialog().getByLabel('Поиск по названию').fill('несуществующая');
  await dialog().getByText('Фотографий по этим условиям нет.').waitFor();
  await dialog().getByLabel('Поиск по названию').fill('выпасе');
  await dialog().getByRole('button', { name: 'Фильтры' }).click();
  await dialog().getByLabel('Ракурс').selectOption('Профиль');
  await dialog().getByLabel('План', { exact: true }).selectOption('Общий план');
  await page.keyboard.press('Escape');
  await dialog()
    .getByRole('button', { name: /Алтай — на выпасе/ })
    .click();
  await snap('review');
  await button('Зафиксировать результат').click();
  assert.equal((await dialog().boundingBox()).width, 440);
  await dialog().getByLabel('Название наблюдения').fill('a/b');
  assert.equal(await dialog().getByRole('button', { name: 'Подтвердить' }).isEnabled(), false);
  await dialog().getByRole('button', { name: 'Отменить' }).click();
  await match('Две особи — первая.jpg');
  await page
    .getByRole('alert')
    .getByText(/ошибка сохранения/i)
    .waitFor();
  await button('Зафиксировать результат').click();
  assert.equal(
    await dialog().getByLabel('Название наблюдения').inputValue(),
    'Две особи — первая.jpg',
  );
  await dialog().getByRole('button', { name: 'Подтвердить' }).click();
  await page.getByRole('heading', { name: 'Новое наблюдение 2' }).waitFor();
  await match('Две особи — вторая.jpg', true);
  await page.getByRole('heading', { name: 'Новое наблюдение', exact: true }).waitFor();
  await nav('Галерея особей');
  await snap('gallery');
  await page.getByRole('button', { name: /Особь 005/ }).click();
  await page.getByRole('button', { name: /Две особи — вторая/ }).click();
  await dialog().getByRole('heading', { name: 'Две особи — вторая.jpg' }).waitFor();
  await page.keyboard.press('Escape');
  await button('Назад в галерею').click();
  await page.getByRole('button', { name: /Алтай/ }).click();
  await page.getByRole('button', { name: /Алтай — на выпасе/ }).click();
  const viewport = await page.evaluate(() => ({ width: innerWidth, height: innerHeight }));
  const bounds = await dialog().boundingBox();
  assert.equal(bounds.width, viewport.width);
  assert.equal(bounds.height, viewport.height);
  await dialog().getByRole('switch', { name: 'Показать трейты' }).click();
  await snap('viewer');
  await page.keyboard.press('ArrowRight');
  await dialog().getByRole('heading', { name: 'Алтай — у склона.jpg' }).waitFor();
  await button('Следующее фото').click();
  await dialog().getByRole('heading', { name: 'Две особи — первая.jpg' }).waitFor();
  await page.keyboard.press('ArrowLeft');
  await page.keyboard.press('Escape');
  await nav('Наблюдения');
  await button('Ручная запись').click();
  await page.getByLabel('Название наблюдения').fill('Полевой след');
  await page.getByLabel('Значение признака', { exact: true }).fill('Тёмная');
  await button('Подставить демокоординаты').click();
  await button('Сохранить запись').click();
  await button('Фильтры').click();
  await page.getByRole('checkbox').check();
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: /Полевой след/ }).waitFor();
  await nav('Синхронизация');
  const connection = page.getByRole('switch', { name: 'Связь с демо-узлом' });
  await connection.click();
  await page.getByText('Офлайн', { exact: true }).waitFor();
  await button('Синхронизировать').click();
  await button('Повторить отправку').waitFor();
  await connection.click();
  await page.getByText('На связи', { exact: true }).waitFor();
  await button('Повторить отправку').click();
  await page.getByText('Все локальные записи учтены.').waitFor();
  await nav('Распознавание');
  const sample = path.join(project, 'public/photos/single.jpg');
  const input = page.getByLabel('Файл наблюдения');
  await input.setInputFiles({
    name: 'bad.jpg',
    mimeType: 'image/jpeg',
    buffer: Buffer.from('invalid'),
  });
  await page
    .getByRole('alert')
    .getByText(/Не удалось прочитать/)
    .waitFor();
  const imageFormats = process.platform === 'darwin' ? ['tiff', 'heic'] : ['jpeg'];
  for (const ext of imageFormats) {
    const imported = path.join(profile, 'проверка.' + ext);
    if (process.platform === 'darwin')
      execFileSync('/usr/bin/sips', ['-s', 'format', ext, sample, '--out', imported], {
        stdio: 'pipe',
      });
    const imgPath = process.platform === 'darwin' ? imported : sample;
    await input.setInputFiles(imgPath);
    await page.getByText(/иллюстративные признаки/).waitFor();
    await match('Загруженное фото.' + ext);
    await page.getByRole('heading', { name: 'Новое наблюдение', exact: true }).waitFor();
  }
  // Real DOM file drops exercise both the multi-file rejection and the valid path.
  const jpeg = (await readFile(sample)).toString('base64');
  async function drop(count) {
    await page.locator('.dropzone').evaluate(
      (el, { jpeg, count }) => {
        const bytes = Uint8Array.from(atob(jpeg), (ch) => ch.charCodeAt(0));
        const dt = new DataTransfer();
        for (let i = 0; i < count; i++)
          dt.items.add(new File([bytes], 'drop-' + i + '.jpg', { type: 'image/jpeg' }));
        el.dispatchEvent(
          new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: dt }),
        );
      },
      { jpeg, count },
    );
  }
  await drop(2);
  await page
    .getByRole('alert')
    .getByText(/одну фотографию/)
    .waitFor();
  await drop(1);
  await page.getByText(/иллюстративные признаки/).waitFor();
  await match('Перетянутый снимок.jpg');
  await nav('Сводка');
  await page.getByText('TAKHI-FIELD-01').waitFor();
  await app.close();
  app = null;
  await launch();
  await nav('Галерея особей');
  await page.getByRole('button', { name: /Сарана/ }).click();
  await page.getByRole('button', { name: /Перетянутый снимок/ }).click();
  await page.waitForFunction(() => {
    const img = document.querySelector('[role=dialog] .marked img');
    return img?.complete && img.naturalWidth > 0;
  });
  await page.keyboard.press('Escape');
  await nav('Настройки');
  await button('Сбросить демо').click();
  await dialog().getByRole('button', { name: 'Восстановить', exact: true }).click();
  await dialog().waitFor({ state: 'hidden' });
  await nav('Галерея особей');
  assert.equal(await page.locator('.herd-card').count(), 4);
  await nav('Распознавание');
  await page.getByRole('button', { name: /На выпасе/ }).click();
  await page.getByRole('heading', { name: 'Новое наблюдение 1' }).waitFor();
  assert.equal(await button('Лошадь — вперёд').isDisabled(), true);
  assert.deepEqual(errors, []);
  assert.deepEqual(network, []);
  console.log(
    'Desktop walkthrough passed: review, recovery, gallery, manual entry, queue, imports, restart, reset and offline transport.',
  );
} finally {
  if (app) await app.close();
  await rm(profile, { recursive: true, force: true });
}
