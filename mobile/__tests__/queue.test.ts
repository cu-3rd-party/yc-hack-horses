// Тесты офлайн-очереди (AsyncStorage). Мок — официальный из пакета async-storage.
jest.mock('@react-native-async-storage/async-storage', () =>
  require('@react-native-async-storage/async-storage/jest/async-storage-mock'),
);

import {
  enqueue,
  flushQueue,
  loadQueue,
  markAttempt,
  type Executor,
} from '../src/features/offline-queue/model/queue';
import { NodeUnavailableError } from '../src/shared/api/client';

beforeEach(async () => {
  // AsyncStorageMock держит состояние в памяти модуля — чистим между тестами.
  const mock = require('@react-native-async-storage/async-storage');
  await (mock.default ?? mock).clear();
});

test('enqueue сохраняет действие и loadQueue возвращает его', async () => {
  const action = await enqueue('observation.create', { payload: { observer_id: 'a' } });
  expect(action.kind).toBe('observation.create');
  expect(action.attempts).toBe(0);

  const queue = await loadQueue();
  expect(queue).toHaveLength(1);
  expect(queue[0]?.id).toBe(action.id);
});

test('flushQueue исполняет действия и удаляет их из очереди', async () => {
  await enqueue('horse.create', { metadata: { name: 'Мадам Пуассон' } });
  await enqueue('observation.create', { payload: { observer_id: 'a' }, local: false });

  const executed: string[] = [];
  const exec: Executor = async (kind) => {
    executed.push(kind);
  };
  const result = await flushQueue(exec);

  expect(executed).toHaveLength(2);
  expect(result).toEqual({ sent: 2, failed: 0 });
  expect(await loadQueue()).toHaveLength(0);
});

test('NodeUnavailableError прерывает прогон и не теряет действия', async () => {
  await enqueue('horse.create', { metadata: {} });
  await enqueue('horse.patch', { id: 'x', payload: {} });

  const exec: Executor = async (kind) => {
    if (kind === 'horse.patch') throw new NodeUnavailableError(new Error('timeout'));
  };
  const result = await flushQueue(exec);

  // первое действие отправлено, второе осталось в очереди с пометкой попытки
  expect(result.sent).toBe(1);
  const queue = await loadQueue();
  expect(queue).toHaveLength(1);
  expect(queue[0]?.kind).toBe('horse.patch');
  expect(queue[0]?.attempts).toBe(1);
  expect(queue[0]?.last_error).toContain('недоступна');
});

test('markAttempt инкрементирует попытки и пишет последнюю ошибку', async () => {
  const action = await enqueue('horse.create', {});
  await markAttempt(action.id, 'HTTP 500 POST /api/v1/horses');
  const queue = await loadQueue();
  expect(queue[0]?.attempts).toBe(1);
  expect(queue[0]?.last_error).toBe('HTTP 500 POST /api/v1/horses');
});
