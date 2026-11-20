// Отправка наблюдения на ноду или в офлайн-очередь.
import type { ObservationCreatePayload } from '@yc-hack-horses/contracts';
import { createObservation, uploadBlob } from '../../../shared/api/endpoints';
import { enqueue } from '../../offline-queue/model/queue';

export interface SaveResult {
  saved: 'online' | 'queued';
  error?: string;
}

export async function saveObservation(
  payload: ObservationCreatePayload,
  pendingFiles: File[],
): Promise<SaveResult> {
  try {
    // Сначала блобы, затем наблюдение со ссылками на них.
    const blobIds: string[] = [];
    for (const file of pendingFiles) {
      const blob = await uploadBlob(file);
      blobIds.push(blob.id);
    }
    await createObservation({ ...payload, blob_ids: blobIds });
    return { saved: 'online' };
  } catch (err) {
    if (err instanceof Error && err.name === 'NodeUnavailableError') {
      // Действие сохраняется локально и уйдёт, когда нода поднимется.
      enqueue('observation.create', { payload, files: pendingFiles.map((f) => f.name) });
      return { saved: 'queued', error: 'Нода недоступна — действие в офлайн-очереди' };
    }
    return {
      saved: 'queued',
      error: err instanceof Error ? err.message : String(err),
    };
  }
}
