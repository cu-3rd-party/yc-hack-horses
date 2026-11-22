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
async function launch(enableDemo = false) {
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
  if (enableDemo) {
    await nav('Настройки');
    const mode = page.getByRole('switch', { name: 'Демосценарии', exact: true });
    assert.equal(await mode.getAttribute('aria-checked'), 'false');
    await mode.click();
    await nav('Распознавание');
  }
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
  if (creating) {
    assert.equal(await dialog().getByRole('button', { name: 'Подтвердить' }).isDisabled(), true);
    const horseName = dialog().getByLabel('Имя лошади');
    await horseName.fill('Искра');
    assert.deepEqual(
      await horseName.evaluate((el) => {
        const style = getComputedStyle(el);
        return [style.borderTopWidth, style.outlineStyle, style.boxShadow];
      }),
      ['1px', 'none', 'none'],
    );
  }
  if (name) await dialog().getByLabel('Название наблюдения').fill(name);
  await dialog().getByRole('button', { name: 'Подтвердить' }).click();
  await dialog().waitFor({ state: 'hidden' });
}
try {
  await launch(true);
  assert.equal(await page.title(), 'ЛошID · Полевой журнал');
  await button('ЛошID — распознавание').waitFor();
  await snap('home');
  await page.evaluate(() => {
    window.dialogAnimations = [];
    document.addEventListener('animationstart', (e) => {
      if (e.target.matches('.dialog,.veil,.toast')) window.dialogAnimations.push(e.animationName);
    });
  });
  const sidebar = page.getByRole('complementary');
  const expandedBounds = await sidebar.boundingBox();
  const workspace = await page.getByRole('main').boundingBox();
  assert.ok(expandedBounds.x + expandedBounds.width <= workspace.x);
  await button('Свернуть меню').click();
  assert.equal((await sidebar.boundingBox()).width, 80);
  await nav('Галерея особей');
  assert.equal(await button('Галерея особей').getAttribute('aria-current'), 'page');
  await nav('Распознавание');
  await button('Развернуть меню').click();
  assert.equal((await sidebar.boundingBox()).width, expandedBounds.width);
  await button('Справка').click();
  await dialog().getByRole('heading', { name: 'Как пользоваться журналом' }).waitFor();
  await page.waitForFunction(() =>
    ['fade-in', 'surface-in'].every((name) => window.dialogAnimations.includes(name)),
  );
  await page.keyboard.press('Escape');
  await page.locator('.veil').waitFor({ state: 'detached' });
  const animated = await page.evaluate(() => window.dialogAnimations);
  for (const expected of ['fade-in', 'fade-out', 'surface-in', 'surface-out'])
    assert.ok(animated.includes(expected), expected);
  await page.locator('.recent').first().click();
  await dialog().waitFor();
  await dialog().getByRole('button', { name: 'Открыть фото на весь экран' }).click();
  await page.locator('.dialog.fullscreen').waitFor();
  await page.keyboard.press('Escape');
  await dialog().getByRole('button', { name: 'Открыть фото на весь экран' }).waitFor();
  await page.keyboard.press('Escape');
  await nav('Распознавание');
  await nav('Настройки');
  assert.equal(await page.getByRole('radio').count(), 2);
  await page.evaluate(() => localStorage.setItem('takhi.color-scheme', 'blue'));
  await page.reload();
  await nav('Настройки');
  const lightScheme = page.getByRole('radio', { name: 'Светлая' });
  assert.equal(await page.locator('html').getAttribute('data-scheme'), 'light');
  assert.equal(await lightScheme.isChecked(), true);
  await lightScheme.focus();
  await page.keyboard.press('ArrowRight');
  assert.equal(await page.getByRole('radio', { name: 'Тёмная' }).isChecked(), true);
  await page.reload();
  await page.getByRole('heading', { name: 'Новое наблюдение', exact: true }).waitFor();
  assert.equal(await page.locator('html').getAttribute('data-scheme'), 'dark');
  await snap('home-dark');
  await nav('Настройки');
  await page.getByRole('radio', { name: 'Светлая' }).check();
  await page.getByRole('switch', { name: 'Сбой один раз' }).click();
  await page.waitForFunction(() =>
    [...document.querySelectorAll('[role=switch]')].some(
      (el) =>
        el.textContent.includes('Сбой один раз') && el.getAttribute('aria-checked') === 'true',
    ),
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
  const toggle = page.getByRole('switch', { name: 'Показать черты' });
  await toggle.click();
  assert.equal(await toggle.getAttribute('aria-checked'), 'false');
  await toggle.click();
  await page.getByRole('button', { name: /Алтай.*83%/ }).click();
  await button('Фото — вперёд').click();
  await page.getByText('Фото 2/2', { exact: true }).waitFor();
  assert.match(await leftPhoto.getAttribute('alt'), /Светлая морда/);
  assert.equal(await page.locator('.trait-key,.open-photo').count(), 0);
  await button('Все фото особи').click();
  await dialog().getByLabel('Поиск по названию').fill('несуществующая');
  await dialog().getByText('Фотографий по этим условиям нет.').waitFor();
  await dialog().getByLabel('Поиск по названию').fill('выпасе');
  const angle = dialog().getByRole('combobox', { name: 'Ракурс' });
  const framing = dialog().getByRole('combobox', { name: 'План' });
  const quality = dialog().getByRole('combobox', { name: 'Качество' });
  await angle.click();
  await angle.getByRole('option', { name: 'Профиль' }).click();
  await framing.click();
  assert.equal(await framing.locator('option:checked svg:visible').count(), 1);
  assert.equal(await framing.locator('option:not(:checked) svg:visible').count(), 0);
  await framing.getByRole('option', { name: 'Общий план' }).click();
  await quality.click();
  await quality.getByRole('option', { name: 'Среднее' }).click();
  await dialog().getByText('Фотографий по этим условиям нет.').waitFor();
  await quality.click();
  await page.keyboard.press('Escape');
  assert.equal(await quality.evaluate((el) => el.matches(':open')), false);
  await dialog().getByRole('button', { name: 'Сбросить фильтры' }).click();
  for (const filter of [angle, framing, quality]) assert.equal(await filter.inputValue(), '');
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
  await page.locator('.toast:not(.error)').waitFor();
  await button('Закрыть уведомление').click();
  await page.locator('.toast').waitFor({ state: 'detached' });
  await match('Две особи — вторая.jpg', true);
  await page.getByRole('heading', { name: 'Новое наблюдение', exact: true }).waitFor();
  await nav('Галерея особей');
  await snap('gallery');
  await page.getByRole('button', { name: /Искра/ }).click();
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
  await dialog().getByRole('switch', { name: 'Показать черты' }).click();
  await snap('viewer');
  await page.keyboard.press('ArrowRight');
  await dialog().getByRole('heading', { name: 'Алтай — у склона.jpg' }).waitFor();
  await button('Следующее фото').click();
  await dialog().getByRole('heading', { name: 'Две особи — первая.jpg' }).waitFor();
  await page.keyboard.press('ArrowLeft');
  await page.keyboard.press('Escape');
  await nav('Наблюдения');
  await button('Ручная запись').click();
  const horseSelect = page.getByRole('combobox', { name: 'Особь' });
  const arrowInset = await horseSelect.evaluate(
    (el) =>
      el.getBoundingClientRect().right -
      el.querySelector('.select-chevron').getBoundingClientRect().right -
      parseFloat(getComputedStyle(el).borderRightWidth),
  );
  assert.equal(arrowInset, 12);
  await horseSelect.click();
  await horseSelect.getByRole('option', { name: 'Сарана' }).click();
  assert.equal(await horseSelect.inputValue(), 'saran');
  await horseSelect.click();
  await horseSelect.getByRole('option', { name: 'Без привязки' }).click();
  await page.getByLabel('Название наблюдения').fill('Полевой след');
  await page.getByLabel('Комментарий').fill('След у воды');
  await page.getByLabel('Значение признака', { exact: true }).fill('Тёмная');
  await button('Подставить демокоординаты').click();
  await button('Сохранить запись').click();
  const unlinked = button('Только без привязки');
  await unlinked.click();
  assert.equal(await unlinked.getAttribute('aria-pressed'), 'true');
  await page.getByRole('button', { name: /Полевой след/ }).click();
  await dialog().getByText('След у воды', { exact: true }).waitFor();
  await page.keyboard.press('Escape');
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
  // A multi-file drop advances only after each photo has been reviewed.
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
  await page.getByText('Снимок 1 из 2 · drop-0.jpg').waitFor();
  await page.getByText(/иллюстративные признаки/).waitFor();
  await match('Первая из очереди.jpg');
  await page.getByText('Снимок 2 из 2 · drop-1.jpg').waitFor();
  await page.getByRole('heading', { name: 'Сравнение наблюдений' }).waitFor();
  await match('Перетянутый снимок.jpg');
  await nav('Сводка');
  await page.getByText('TAKHI-FIELD-01').waitFor();
  await app.close();
  app = null;
  await launch();
  await nav('Галерея особей');
  await page.getByRole('button', { name: /Сарана/ }).click();
  const galleryQuality = page.getByRole('combobox', { name: 'Качество' });
  await galleryQuality.click();
  await galleryQuality.getByRole('option', { name: 'Без оценки' }).click();
  await page.getByRole('button', { name: /Сарана — на выпасе/ }).waitFor({ state: 'detached' });
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
  await button('Отменить сравнение').click();
  await dialog().getByText('Оставшиеся наблюдения будут пропущены').waitFor();
  await dialog().getByRole('button', { name: 'Отменить', exact: true }).click();
  await dialog().waitFor({ state: 'hidden' });
  await page.getByRole('heading', { name: 'Сравнение наблюдений' }).waitFor();
  await button('Отменить сравнение').click();
  await dialog().getByRole('button', { name: 'Подтвердить' }).click();
  await dialog().waitFor({ state: 'hidden' });
  await page.getByRole('heading', { name: 'Новое наблюдение', exact: true }).waitFor();
  assert.deepEqual(errors, []);
  assert.deepEqual(network, []);
  console.log(
    'Desktop walkthrough passed: review, recovery, gallery, manual entry, queue, imports, restart, reset and offline transport.',
  );
} finally {
  if (app) await app.close();
  await rm(profile, { recursive: true, force: true });
}
