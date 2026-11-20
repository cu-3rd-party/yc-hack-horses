// Очередь неотправленных действий (offline-first). Действие кладётся в
// localStorage при недоступности ноды и выгружается, когда нода отвечает.

export interface QueuedAction {
  id: string;
  kind: 'observation.create' | 'horse.create' | 'horse.patch' | 'blob.upload';
  payload: unknown;
  created_at: string;
  attempts: number;
  last_error: string | null;
}

const KEY = 'yc-hack-horses.offline-queue.v1';

export function loadQueue(): QueuedAction[] {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? (parsed as QueuedAction[]) : [];
  } catch {
    return [];
  }
}

export function saveQueue(actions: QueuedAction[]): void {
  localStorage.setItem(KEY, JSON.stringify(actions));
}

export function enqueue(kind: QueuedAction['kind'], payload: unknown): QueuedAction {
  const action: QueuedAction = {
    id: `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`,
    kind,
    payload,
    created_at: new Date().toISOString(),
    attempts: 0,
    last_error: null,
  };
  saveQueue([...loadQueue(), action]);
  return action;
}

export function removeAction(id: string): void {
  saveQueue(loadQueue().filter((a) => a.id !== id));
}

export function markAttempt(id: string, error: string | null): void {
  const actions = loadQueue();
  const next = actions.map((a) =>
    a.id === id ? { ...a, attempts: a.attempts + 1, last_error: error } : a,
  );
  saveQueue(next);
}

export interface Executor {
  (kind: QueuedAction['kind'], payload: unknown): Promise<void>;
}

// Прогон очереди по порядку; действие выполняется, иначе остаётся с ошибкой.
export async function flushQueue(exec: Executor): Promise<{ sent: number; failed: number }> {
  const actions = loadQueue();
  let sent = 0;
  let failed = 0;
  for (const action of actions) {
    try {
      await exec(action.kind, action.payload);
      removeAction(action.id);
      sent += 1;
    } catch (err) {
      failed += 1;
      markAttempt(action.id, err instanceof Error ? err.message : String(err));
      if (err instanceof Error && err.name === 'NodeUnavailableError') break;
    }
  }
  return { sent, failed };
}
