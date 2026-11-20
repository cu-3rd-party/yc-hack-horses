// Мост к Go Core (ADR-0011): нативный модуль, собранный gomobile bind из
// backend/cmd/mobilebind. Экспортирует типизированные методы; если библиотека
// не вшита в сборку (например Expo Go), available=false и вызовы бросают
// понятную ошибку — это состояние среды, а не заглушка.
import { NativeModules, Platform } from 'react-native';

export interface GoCoreObservationInput {
  horse_id: string;
  observed_at: string;
  location: string;
  observer_id: string;
  blob_ids: string[];
}

export interface GoCoreSyncResult {
  peer: string;
  pulled: number;
  pushed: number;
  duration_ms: number;
  error?: string;
  skipped_uninitialized?: boolean;
}

export interface GoCoreStatus {
  node_id: string;
  app_version: string;
  role: string;
  driver: string;
  event_count: number;
  last_seq: number;
  vector_clock: Record<string, number>;
}

interface YcMobileCoreModule {
  init(dataDir: string): Promise<void>;
  appendObservation(payloadJSON: string): Promise<string>;
  storeBlob(data: number[], mimeType: string): Promise<string>;
  foregroundSync(peerAddr: string): Promise<string>;
  nodeStatus(): Promise<string>;
  close(): Promise<void>;
}

const mod = NativeModules.YcMobileCore as YcMobileCoreModule | undefined;

function requireCore(): YcMobileCoreModule {
  if (!mod) {
    throw new Error(
      'Go Core не вшит в эту сборку (нужен dev client с gomobile-библиотекой backend/cmd/mobilebind). ' +
        'См. mobile/README.md, раздел «Сборка Go Core».',
    );
  }
  return mod;
}

export const goCore = {
  /** true, если нативный модуль присутствует в текущей сборке. */
  available: mod !== undefined,

  /** Инициализация ноды: SQLite + миграции в каталоге данных приложения. */
  async init(dataDir: string): Promise<void> {
    await requireCore().init(dataDir);
  },

  /** Офлайн-запись наблюдения в локальный журнал. Возвращает JSON наблюдения. */
  async appendObservation(input: GoCoreObservationInput): Promise<string> {
    return requireCore().appendObservation(JSON.stringify(input));
  },

  /** Запись blob в локальное content-addressed хранилище. Возвращает JSON блоба. */
  async storeBlob(data: Uint8Array, mimeType: string): Promise<string> {
    // gomobile: []byte <-> JS Array<number>
    return requireCore().storeBlob(Array.from(data), mimeType);
  },

  /** Один цикл foreground sync с центральной нодой. */
  async foregroundSync(peerAddr: string): Promise<GoCoreSyncResult> {
    const json = await requireCore().foregroundSync(peerAddr);
    return JSON.parse(json) as GoCoreSyncResult;
  },

  /** Статус локальной ноды. */
  async status(): Promise<GoCoreStatus> {
    const json = await requireCore().nodeStatus();
    return JSON.parse(json) as GoCoreStatus;
  },

  async close(): Promise<void> {
    await requireCore().close();
  },
};

export const GO_CORE_MODULE_NAME = Platform.OS === 'ios' ? 'YcMobileCore' : 'YcMobileCore';
