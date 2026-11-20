// Исполнители офлайн-очереди: переводят действие в вызов ноды.
import type { QueuedAction } from './queue';
import { createObservation, createHorse, patchHorse } from '../../../shared/api/endpoints';

export async function executeQueuedAction(kind: QueuedAction['kind'], payload: unknown): Promise<void> {
  switch (kind) {
    case 'observation.create': {
      const p = payload as { payload?: unknown; files?: string[] };
      await createObservation(p.payload as Parameters<typeof createObservation>[0]);
      return;
    }
    case 'horse.create':
      await createHorse(payload as Parameters<typeof createHorse>[0]);
      return;
    case 'horse.patch': {
      const p = payload as { id: string; payload: unknown };
      await patchHorse(p.id, p.payload as Parameters<typeof patchHorse>[1]);
      return;
    }
    case 'blob.upload':
      // Файлы в очереди не сериализуются — загрузка блобов повторяется
      // пользователем при следующем редактировании наблюдения.
      return;
    default:
      throw new Error(`Неизвестный тип действия: ${kind}`);
  }
}
