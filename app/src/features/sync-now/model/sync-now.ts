// Ручной запуск синхронизации. Эндпоинт ноды /api/v1/node/sync (V0):
// ответ ноды не специфицирован — важен только успех/ошибка.
import { api } from '../../../shared/api/endpoints';

export async function syncNow(): Promise<void> {
  await api.request('/api/v1/node/sync', { method: 'POST', timeoutMs: 10000 });
}
