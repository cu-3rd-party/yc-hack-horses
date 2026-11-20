// Логика создания наблюдения: фото через expo-image-picker -> локальный файл ->
// blob в Go Core (при наличии) или multipart на ноду -> наблюдение в Go Core
// или офлайн-очередь.
import * as ImagePicker from 'expo-image-picker';
import { File } from 'expo-file-system';
import { goCore } from '../../../shared/go-core/bridge';
import { uploadBlob, createObservation } from '../../../shared/api/endpoints';
import { enqueue } from '../../offline-queue/model/queue';
import { NodeUnavailableError } from '../../../shared/api/client';
import type { ObservationCreatePayload } from '@yc-hack-horses/contracts';

export interface PickedPhoto {
  uri: string;
  name: string;
  mimeType: string;
}

export async function pickPhoto(): Promise<PickedPhoto | null> {
  const perm = await ImagePicker.requestMediaLibraryPermissionsAsync();
  if (!perm.granted) {
    throw new Error('Нет доступа к медиатеке — снимок выбрать нельзя');
  }
  const result = await ImagePicker.launchImageLibraryAsync({
    mediaTypes: ['images'],
    quality: 0.8,
  });
  if (result.canceled || result.assets.length === 0) return null;
  const asset = result.assets[0]!;
  const ext = asset.mimeType === 'image/png' ? 'png' : 'jpg';
  return {
    uri: asset.uri,
    name: `observation-${Date.now()}.${ext}`,
    mimeType: asset.mimeType ?? 'image/jpeg',
  };
}

export async function readPhotoBytes(photo: PickedPhoto): Promise<Uint8Array> {
  // SDK 58: новый API expo-file-system, legacy readAsStringAsync удалён.
  return new File(photo.uri).bytes();
}

export interface SaveResult {
  where: 'go-core' | 'node' | 'queue';
  message: string;
}

// Полный путь сохранения наблюдения. Офлайн-случай: наблюдение уходит в
// AsyncStorage-очередь и будет доставлено при следующем flush.
export async function saveObservation(
  photo: PickedPhoto | null,
  observerId: string,
  horseId: string | null,
  location: string,
): Promise<SaveResult> {
  if (!observerId.trim()) {
    throw new Error('observer_id обязателен');
  }
  const payload: ObservationCreatePayload = {
    horse_id: horseId,
    observed_at: new Date().toISOString(),
    observer_id: observerId.trim(),
    blob_ids: [],
  };

  if (photo) {
    if (goCore.available) {
      const bytes = await readPhotoBytes(photo);
      const blob = JSON.parse(await goCore.storeBlob(bytes, photo.mimeType)) as { sha256: string };
      payload.blob_ids = [blob.sha256];
    } else {
      try {
        // Сеть есть и Go Core нет: multipart прямо на ноду.
        const up = await uploadBlob({ uri: photo.uri, name: photo.name, type: photo.mimeType });
        payload.blob_ids = [up.sha256];
      } catch (err) {
        if (err instanceof NodeUnavailableError) {
          // Офлайн: файл остаётся в каталоге приложения, наблюдение — в очереди;
          // при следующем sync блоб догружается нодой из Go Core или повторно.
          await enqueue('observation.create', { payload, local: goCore.available });
          return { where: 'queue', message: 'Нода недоступна: наблюдение в офлайн-очереди' };
        }
        throw err;
      }
    }
  }

  if (goCore.available) {
    const obsJSON = await goCore.appendObservation({
      horse_id: horseId ?? '',
      observed_at: payload.observed_at ?? '',
      location,
      observer_id: payload.observer_id ?? '',
      blob_ids: payload.blob_ids ?? [],
    });
    return { where: 'go-core', message: `Записано в Go Core: ${obsJSON.slice(0, 60)}…` };
  }

  try {
    const obs = await createObservation(payload);
    return { where: 'node', message: `Наблюдение ${obs.id} записано на ноду` };
  } catch (err) {
    if (err instanceof NodeUnavailableError) {
      await enqueue('observation.create', { payload, local: false });
      return { where: 'queue', message: 'Нода недоступна: наблюдение в офлайн-очереди' };
    }
    throw err;
  }
}
