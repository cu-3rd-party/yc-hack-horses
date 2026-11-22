// The caller owns the disposable Go node and the browser or Electron window.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

export async function checkPassports(page, origin, client) {
  async function create(name, metadata = {}) {
    const response = await fetch(origin + '/api/v1/horses', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ metadata: { name: client + ' ' + name, ...metadata } }),
    });
    assert.ok(response.ok);
    return (await response.json()).id;
  }
  async function attach(id, bytes) {
    const data = new FormData();
    data.append('file', new Blob([bytes], { type: 'application/pdf' }), 'паспорт.pdf');
    const response = await fetch(origin + '/api/v1/horses/' + id + '/passport', { method: 'PUT', body: data });
    assert.ok(response.ok);
  }

  const atlas = await create('Атлас');
  const orion = await create('Орион', { passport_sha256: 'f'.repeat(64) });
  const missing = await create('Без паспорта');
  const corrupt = await create('Повреждённый');
  await attach(atlas, await readFile(new URL('./fixtures/passport-atlas.pdf', import.meta.url)));
  await attach(corrupt, '%PDF-1.4\ninvalid');
  const requested = new Set();
  page.on('request', request => {
    const match = new URL(request.url()).pathname.match(/^\/api\/v1\/horses\/([^/]+)\/passport$/);
    if (match) requested.add(match[1]);
  });

  await page.reload();
  const button = name => page.getByRole('button', { name, exact: true });
  const open = button('Открыть паспорт');
  const back = button('Назад к особям');
  const dialog = page.getByRole('dialog');
  const choose = name => page.getByText(client + ' ' + name, { exact: true }).click();
  const close = async () => {
    await page.keyboard.press('Escape');
    await dialog.waitFor({ state: 'hidden' });
    assert.ok(await open.evaluate(el => el === document.activeElement));
  };
  await button('Особи').click();
  await choose('Без паспорта');
  assert.ok(await open.isDisabled());
  await page.getByText('Паспорт не прикреплён').waitFor();
  assert.equal(requested.size, 0);
  await back.click();
  await choose('Атлас');
  const search = page.getByRole('textbox', { name: 'Поиск по названию' });
  await search.fill('снимок');
  await open.click();
  await dialog.getByText('Passport Atlas - page 1', { exact: true }).waitFor();
  const firstPage = dialog.locator('.page').first();
  const width = await firstPage.evaluate(el => el.clientWidth);
  await button('Увеличить масштаб').click();
  assert.ok(await firstPage.evaluate(el => el.clientWidth) > width);
  await button('По ширине').click();
  await firstPage.locator('canvas').first().waitFor();
  const scroller = dialog.getByRole('region', { name: 'Страницы паспорта' });
  await scroller.evaluate(el => el.scrollTop = el.scrollHeight);
  await dialog.getByText('Passport Atlas - page 2', { exact: true }).waitFor();
  assert.ok(await scroller.evaluate(el => el.scrollTop > 0));
  await close();
  assert.equal(await search.inputValue(), 'снимок');
  await back.click();
  await choose('Орион');
  await open.click();
  await dialog.getByText('Файл паспорта пока недоступен').waitFor();
  await attach(orion, await readFile(new URL('./fixtures/passport-orion.pdf', import.meta.url)));
  await button('Повторить').click();
  await dialog.getByText('Passport Orion - page 1', { exact: true }).waitFor();
  assert.equal(await dialog.locator('.page').count(), 1);
  await button('Закрыть').click();
  await dialog.waitFor({ state: 'hidden' });
  await back.click();
  await choose('Повреждённый');
  await open.click();
  await dialog.getByText('Не удалось открыть PDF').waitFor();
  await close();
  assert.ok(!requested.has(missing));
  console.log(client + ': separate passports, zoom, scroll, empty state, retry and corrupt PDF passed.');
}
