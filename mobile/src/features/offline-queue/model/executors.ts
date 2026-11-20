// Исполнители офлайн-очереди: переводят действие в вызов ноды.
// Приоритет отправки: Go Core (локальная запись в SQLite) — если недоступен,
// REST ноды. blob.upload в очереди не сериализуется — файлы подхватываются
// при следующем foreground sync из локального хранилища Go Core.
import type { QueuedAction } from './queue';
import { createObservation, createHorse, patchHorse } from '../../../shared/api/endpoints';
import { goCore } from '../../../shared/go-core/bridge';
import type { ObservationCreatePayload, HorseUpsertPayload } from '@yc-hack-horses/contracts';

export async function executeQueuedAction(
  kind: QueuedAction['kind'],
  payload: unknown,
): Promise<void> {
  switch (kind) {
    case 'observation.create': {
      const p = payload as { payload?: ObservationCreatePayload; local?: boolean };
      if (p.local && goCore.available) {
        // Локальная запись через Go Core: наблюдение уже не "отправка", а
        // фиксация в собственном журнале ноды устройства.
        await goCore.appendObservation({
          horse_id: p.payload?.horse_id ?? '',
          observed_at: p.payload?.observed_at ?? '',
          location: '',
          observer_id: p.payload?.observer_id ?? '',
          blob_ids: p.payload?.blob_ids ?? [],
        });
        return;
      }
      await createObservation((p.payload ?? {}) as ObservationCreatePayload);
      return;
    }
    case 'horse.create':
      await createHorse(payload as HorseUpsertPayload);
      return;
    case 'horse.patch': {
      const p = payload as { id: string; payload: HorseUpsertPayload };
      await patchHorse(p.id, p.payload);
      return;
    }
    case 'blob.upload':
      // Файлы в очереди не сериализуются; см. комментарий в заголовке файла.
      return;
    default:
      throw new Error(`Неизвестный тип действия: ${kind satisfies never}`);
  }
}
