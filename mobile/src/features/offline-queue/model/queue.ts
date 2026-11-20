// Очередь неотправленных действий (offline-first) — портирована из
// app/src/features/offline-queue. Хранилище: AsyncStorage вместо localStorage.
// На мобильном устройстве нода часто недоступна (поле, нет сети до сервера),
// поэтому наблюдения сначала пишутся в очередь локально.
import AsyncStorage from '@react-native-async-storage/async-storage';

export interface QueuedAction {
  id: string;
  kind: 'observation.create' | 'horse.create' | 'horse.patch' | 'blob.upload';
  payload: unknown;
  created_at: string;
  attempts: number;
  last_error: string | null;
}

const KEY = 'yc-hack-horses.offline-queue.v1';

export async function loadQueue(): Promise<QueuedAction[]> {
  try {
    const raw = await AsyncStorage.getItem(KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? (parsed as QueuedAction[]) : [];
  } catch {
    return [];
  }
}

export async function saveQueue(actions: QueuedAction[]): Promise<void> {
  await AsyncStorage.setItem(KEY, JSON.stringify(actions));
}

export async function enqueue(kind: QueuedAction['kind'], payload: unknown): Promise<QueuedAction> {
  const action: QueuedAction = {
    id: `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`,
    kind,
    payload,
    created_at: new Date().toISOString(),
    attempts: 0,
    last_error: null,
  };
  await saveQueue([...(await loadQueue()), action]);
  return action;
}

export async function removeAction(id: string): Promise<void> {
  await saveQueue((await loadQueue()).filter((a) => a.id !== id));
}

export async function markAttempt(id: string, error: string | null): Promise<void> {
  const actions = await loadQueue();
  await saveQueue(
    actions.map((a) => (a.id === id ? { ...a, attempts: a.attempts + 1, last_error: error } : a)),
  );
}

export interface Executor {
  (kind: QueuedAction['kind'], payload: unknown): Promise<void>;
}

// Прогон очереди по порядку; действие выполняется, иначе остаётся с ошибкой.
// NodeUnavailableError прерывает прогон — нода недоступна, дальше слать бессмысленно.
export async function flushQueue(exec: Executor): Promise<{ sent: number; failed: number }> {
  const actions = await loadQueue();
  let sent = 0;
  let failed = 0;
  for (const action of actions) {
    try {
      await exec(action.kind, action.payload);
      await removeAction(action.id);
      sent += 1;
    } catch (err) {
      failed += 1;
      await markAttempt(action.id, err instanceof Error ? err.message : String(err));
      if (err instanceof Error && err.name === 'NodeUnavailableError') break;
    }
  }
  return { sent, failed };
}
