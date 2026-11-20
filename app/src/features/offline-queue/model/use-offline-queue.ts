import { useCallback, useState } from 'react';
import type { QueuedAction } from './queue';
import { enqueue, flushQueue, loadQueue } from './queue';

export function useOfflineQueue() {
  const [actions, setActions] = useState<QueuedAction[]>(() => loadQueue());

  const refresh = useCallback(() => setActions(loadQueue()), []);

  const add = useCallback(
    (kind: QueuedAction['kind'], payload: unknown) => {
      enqueue(kind, payload);
      refresh();
    },
    [refresh],
  );

  const flush = useCallback(
    async (exec: Parameters<typeof flushQueue>[0]) => {
      const result = await flushQueue(exec);
      refresh();
      return result;
    },
    [refresh],
  );

  return { actions, pendingCount: actions.length, add, flush, refresh };
}
