// Базовый URL локальной ноды. В dev-браузере проксируется через Vite,
// в Tauri нода слушает 127.0.0.1.
export const NODE_API_BASE =
  typeof import.meta !== 'undefined' && import.meta.env?.VITE_NODE_API_BASE
    ? String(import.meta.env.VITE_NODE_API_BASE)
    : 'http://127.0.0.1:8080';

export const WS_EVENTS_PATH = '/api/v1/events';
