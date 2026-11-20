// Адрес центральной ноды для foreground sync (gRPC host:port).
// Переопределяется через EXPO_PUBLIC_SYNC_PEER; по умолчанию — нода на хосте
// для эмулятора Android.
export const FOREGROUND_SYNC_PEER =
  process.env.EXPO_PUBLIC_SYNC_PEER ?? '10.0.2.2:9090';
