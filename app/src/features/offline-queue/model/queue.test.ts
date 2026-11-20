import { beforeEach, describe, expect, it, vi } from 'vitest';
// Тесты очереди используют localStorage — нужен DOM-стаб.
// @vitest-environment jsdom
import { enqueue, flushQueue, loadQueue, removeAction } from './queue';

beforeEach(() => {
  localStorage.clear();
});

describe('offline queue', () => {
  it('enqueue сохраняет действие в localStorage', () => {
    enqueue('observation.create', { horse_id: null });
    const queue = loadQueue();
    expect(queue).toHaveLength(1);
    expect(queue[0]?.kind).toBe('observation.create');
    expect(queue[0]?.payload).toEqual({ horse_id: null });
  });

  it('flushQueue отправляет действия по порядку и чистит очередь', async () => {
    enqueue('observation.create', { a: 1 });
    enqueue('horse.create', { b: 2 });
    const exec = vi.fn().mockResolvedValue(undefined);
    const result = await flushQueue(exec);
    expect(result.sent).toBe(2);
    expect(result.failed).toBe(0);
    expect(exec).toHaveBeenCalledTimes(2);
    expect(loadQueue()).toHaveLength(0);
  });

  it('flushQueue останавливается на недоступной ноде и сохраняет ошибку', async () => {
    enqueue('observation.create', { a: 1 });
    enqueue('horse.create', { b: 2 });
    const err = new Error('down');
    err.name = 'NodeUnavailableError';
    const exec = vi.fn().mockRejectedValue(err);
    const result = await flushQueue(exec);
    expect(result.sent).toBe(0);
    expect(result.failed).toBe(1);
    expect(loadQueue()).toHaveLength(2);
    expect(loadQueue()[0]?.last_error).toBe('down');
  });

  it('removeAction удаляет конкретное действие', () => {
    const a = enqueue('observation.create', {});
    enqueue('horse.create', {});
    removeAction(a.id);
    expect(loadQueue()).toHaveLength(1);
    expect(loadQueue()[0]?.kind).toBe('horse.create');
  });
});
