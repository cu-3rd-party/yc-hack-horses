// Smoke test for a local image or the deployed journal. Demo decisions stay in this fresh context.
import assert from 'node:assert/strict';
import { chromium } from 'playwright-core';

const url = new URL(process.env.JOURNAL_URL || 'http://127.0.0.1:8080/');
const browser = await chromium.launch({ channel: process.env.JOURNAL_BROWSER });
try {
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const nav = name => page.getByRole('navigation').getByRole('button', { name, exact: true }).click();
  const heading = name => page.getByRole('heading', { name, exact: true }).waitFor();
  // Also guard the production check against accidentally writing to the live API.
  await page.route('**/api/v1/**', async route => {
    if (route.request().method() === 'GET') return route.continue();
    errors.push('Unexpected live API write: ' + route.request().method());
    return route.abort();
  });
  const horses = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/horses' && r.ok());
  await page.goto(url.href);
  const catalog = await (await horses).json();
  await heading('Новое наблюдение');
  await nav('Особи');
  await heading('Особи');
  await page.getByText(catalog.length + ' в каталоге', { exact: true }).waitFor();
  await nav('Настройки');
  await page.getByText('Сервер журнала · ' + url.host, { exact: true }).waitFor();
  const mode = page.getByRole('switch', { name: 'Демосценарии' });
  assert.equal(await mode.getAttribute('aria-checked'), 'false');
  await mode.click();
  await nav('Распознавание');
  await page.getByRole('button', { name: /На выпасе/ }).click();
  await heading('Сравнение наблюдений');
  const photos = page.locator('.compare-card .marked img');
  assert.equal(await photos.count(), 2);
  for (const photo of await photos.all()) {
    await photo.evaluate(img => img.decode());
  }
  await page.getByRole('button', { name: 'Зафиксировать результат' }).click();
  const dialog = page.getByRole('dialog');
  const name = 'Проверка веб-журнала';
  const comment = 'Следы у ручья.\nУтром.';
  await dialog.getByLabel('Комментарий').fill(comment);
  await dialog.getByLabel('Название наблюдения').fill(name);
  await dialog.getByRole('button', { name: 'Подтвердить' }).click();
  await heading('Новое наблюдение');
  await nav('Наблюдения');
  await page.getByText(name, { exact: true }).first().click();
  assert.equal(await dialog.locator('dd.observation-comment').textContent(), comment);
  assert.equal(await dialog.getByRole('button', { name: 'Удалить наблюдение' }).isDisabled(), true);
  assert.deepEqual(errors, []);
  console.log('Live catalog, settings, demo comparison, photos and saved observation passed.');
} finally {
  await browser.close();
}
