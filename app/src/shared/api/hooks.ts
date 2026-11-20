import { useEffect, useRef, useState, useCallback } from 'react';
import { NODE_API_BASE, WS_EVENTS_PATH } from '../config/node';
import type { WsEvent } from '@yc-hack-horses/contracts';

export type ConnectionStatus = 'connecting' | 'online' | 'offline';

// Подписка на стрим событий ноды через WebSocket. При разрыве — reconnect
// с экспоненциальной задержкой, статус соединения для UI.
export function useNodeEvents(onEvent: (event: WsEvent) => void) {
  const [status, setStatus] = useState<ConnectionStatus>('connecting');
  const handlerRef = useRef(onEvent);
  handlerRef.current = onEvent;

  useEffect(() => {
    let ws: WebSocket | null = null;
    let closed = false;
    let attempt = 0;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const connect = () => {
      if (closed) return;
      setStatus('connecting');
      const url = `${NODE_API_BASE.replace(/^http/, 'ws')}${WS_EVENTS_PATH}`;
      try {
        ws = new WebSocket(url);
      } catch {
        scheduleRetry();
        return;
      }
      ws.onopen = () => {
        attempt = 0;
        setStatus('online');
      };
      ws.onmessage = (msg) => {
        try {
          handlerRef.current(JSON.parse(String(msg.data)) as WsEvent);
        } catch {
          // некорректный кадр — игнорируем
        }
      };
      ws.onclose = () => {
        setStatus('offline');
        scheduleRetry();
      };
      ws.onerror = () => ws?.close();
    };

    const scheduleRetry = () => {
      if (closed) return;
      const delay = Math.min(1000 * 2 ** attempt, 15000);
      attempt += 1;
      timer = setTimeout(connect, delay);
    };

    connect();
    return () => {
      closed = true;
      if (timer) clearTimeout(timer);
      ws?.close();
    };
  }, []);

  return status;
}

export interface AsyncState<T> {
  data: T | null;
  loading: boolean;
  error: Error | null;
  reload: () => void;
}

// Простая загрузка данных с ноды; NodeUnavailableError не роняет экран.
export function useNodeResource<T>(fn: () => Promise<T>, deps: unknown[] = []): AsyncState<T> {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const [tick, setTick] = useState(0);

  const reload = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError(null);
    fn()
      .then((d) => {
        if (alive) setData(d);
      })
      .catch((err: unknown) => {
        if (alive) setError(err instanceof Error ? err : new Error(String(err)));
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [tick, ...deps]);

  return { data, loading, error, reload };
}

export function isNodeUnavailable(err: Error | null): boolean {
  return err?.name === 'NodeUnavailableError';
}
