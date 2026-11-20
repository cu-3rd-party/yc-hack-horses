// Базовый URL локальной ноды. На эмуляторе Android 10.0.2.2 = host loopback;
// на реальном устройстве — LAN-адрес машины с нодой (переопределяется через
// EXPO_PUBLIC_NODE_API_BASE).
export const NODE_API_BASE =
  process.env.EXPO_PUBLIC_NODE_API_BASE ?? 'http://10.0.2.2:8080';

export const WS_EVENTS_PATH = '/api/v1/events';
