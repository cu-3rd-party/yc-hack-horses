// Foreground sync (ADR-0011): только исходящие соединения, запуск по кнопке
// и при открытии приложения. Никакого фонового демона.
import { useCallback, useState } from 'react';
import { goCore, type GoCoreSyncResult } from '../../../shared/go-core/bridge';
import { useOfflineQueue } from '../../offline-queue/model/use-offline-queue';

export interface SyncState {
  running: boolean;
  goCoreResult: GoCoreSyncResult | null;
  queueResult: { sent: number; failed: number } | null;
  error: string | null;
}

export function useForegroundSync() {
  const [state, setState] = useState<SyncState>({
    running: false,
    goCoreResult: null,
    queueResult: null,
    error: null,
  });
  const queue = useOfflineQueue();

  const run = useCallback(
    async (peerAddr: string) => {
      setState((s) => ({ ...s, running: true, error: null }));
      try {
        if (goCore.available) {
          // Foreground sync = журнал устройства <-> центральная нода (gRPC).
          const res = await goCore.foregroundSync(peerAddr);
          setState((s) => ({ ...s, goCoreResult: res, running: false }));
          return;
        }
        // Без Go Core: выгружаем офлайн-очередь REST-вызовами.
        const res = await queue.flush();
        setState((s) => ({ ...s, queueResult: res, running: false }));
      } catch (err) {
        setState((s) => ({
          ...s,
          running: false,
          error: err instanceof Error ? err.message : String(err),
        }));
      }
    },
    [queue],
  );

  return { state, run, queueActions: queue.actions };
}
