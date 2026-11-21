import 'fake-indexeddb/auto';
import { beforeEach, describe, expect, it } from 'vitest';
import { command, getBook, readMedia, validCoordinates } from './api';
import { horsePhotos, validName } from './types';
beforeEach(async () => {
  await command('/reset', undefined);
});
describe('локальный журнал', () => {
  it('разрешает каждую особь отдельно и отклоняет повтор старого решения', async () => {
    const initial = await getBook();
    const book = await command('/analysis', { sample: 'pair' });
    const review = book.review!,
      [left, right] = review.detections;
    expect(left!.candidates[0]!.horseId).not.toEqual(right!.candidates[0]!.horseId);
    const match = {
      reviewId: review.id,
      detectionId: left!.id,
      horseId: 'saran',
      name: 'Снимок 1.jpg',
    };
    await command('/preferences', { failNextSave: true });
    await expect(command('/decisions', match)).rejects.toThrow('Демонстрационная ошибка');
    expect((await getBook()).review?.detections).toHaveLength(2);
    const [a, b] = await Promise.allSettled([
      command('/decisions', match),
      command('/decisions', match),
    ]);
    expect([a.status, b.status].sort()).toEqual(['fulfilled', 'rejected']);
    const partial = await getBook();
    expect(partial.review!.detections[0]!.id).toBe(right!.id);
    expect(horsePhotos(partial, 'saran').at(-1)?.annotation).toEqual(left!.annotation);
    const done = await command('/decisions', {
      ...match,
      horseId: null,
      detectionId: right!.id,
      name: 'Снимок 2.jpg',
    });
    expect(done.review).toBeNull();
    expect(done.horses).toHaveLength(initial.horses.length + 1);
    expect(horsePhotos(done, done.horses.at(-1)!.id)[0]!.annotation).toEqual(right!.annotation);
    expect(done.pending).toHaveLength(2);
  });
  it('хранит байты и очередь офлайн, повторяет синхронизацию и очищает медиа при сбросе', async () => {
    await command('/preferences', { online: false });
    const file = {
      filename: 'field.jpg',
      original: new Blob(['original']),
      preview: new Blob(['preview']),
    };
    const added = await command('/manual', {
      horseId: null,
      name: 'Запись',
      coordinates: '42, -110',
      traits: [{ label: 'Уши', value: 'стоячие', confidence: 0.8 }],
      confidence: 0.75,
      files: [file],
    });
    const key = added.observations.at(-1)!.photos[0]!.source;
    expect(await (await readMedia(key))?.preview.text()).toBe('preview');
    await expect(command('/sync', undefined)).rejects.toThrow('недоступен');
    expect((await getBook()).pending).toHaveLength(1);
    await command('/preferences', { online: true });
    expect((await command('/sync', undefined)).sent).toBe(1);
    expect((await command('/sync', undefined)).sent).toBe(1);
    await command('/reset', undefined);
    expect(await readMedia(key)).toBeUndefined();
  });
  it('отклоняет недопустимые названия и координаты', () => {
    for (const value of ['', 'a/b', '..', 'NUL.jpg', 'a:', 'x'.repeat(256)])
      expect(validName(value)).toBe(false);
    expect(validName('У сопки — 2.jpg')).toBe(true);
    expect(validCoordinates('47.78, -106.01')).toBe(true);
    expect(validCoordinates('90.1, 0')).toBe(false);
  });
});
