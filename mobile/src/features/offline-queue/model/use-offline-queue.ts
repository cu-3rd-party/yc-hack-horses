// Реактивная обёртка над очередью: состояние для UI + действия.
import { useCallback, useEffect, useState } from 'react';
import {
  enqueue,
  flushQueue,
  loadQueue,
  type Executor,
  type QueuedAction,
} from './queue';
import { executeQueuedAction } from './executors';

export function useOfflineQueue() {
  const [actions, setActions] = useState<QueuedAction[]>([]);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    setActions(await loadQueue());
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const add = useCallback(
    async (kind: QueuedAction['kind'], payload: unknown) => {
      const action = await enqueue(kind, payload);
      await refresh();
      return action;
    },
    [refresh],
  );

  const flush = useCallback(
    async (exec: Executor = executeQueuedAction) => {
      setBusy(true);
      try {
        const result = await flushQueue(exec);
        await refresh();
        return result;
      } finally {
        setBusy(false);
      }
    },
    [refresh],
  );

  return { actions, busy, add, flush, refresh };
}
