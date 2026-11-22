import type {
  CancelResult,
  ComparisonDecision,
  ComparisonRequest,
  ComparisonResult,
  DecisionResult,
  HorseAnnotation,
  NodeObservation,
  ObservationPhoto,
} from '../../../packages/contracts/src/comparison';
import {
  noMarks,
  type Annotation,
  type Decision,
  type ImportPhoto,
  type ManualInput,
  type NodeInfo,
  type Notebook,
  type Observation,
  type Photo,
  type Review,
} from './types';

const root = '/api/v1';
export const desktop = globalThis.location?.protocol === 'journal:';
const unavailable = desktop ? 'Локальная нода недоступна' : 'Сервер журнала недоступен';
class NodeError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly code?: string,
  ) {
    super(message);
  }
}
async function request<T>(
  path: string,
  method = 'GET',
  body?: object,
  signal?: AbortSignal,
): Promise<T> {
  const multipart = body instanceof FormData;
  let response: Response;
  const timeout = AbortSignal.timeout(method === 'GET' ? 10000 : 120000);
  try {
    response = await fetch(root + path, {
      method,
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
      headers: body && !multipart ? { 'Content-Type': 'application/json' } : undefined,
      body: multipart ? body : body ? JSON.stringify(body) : undefined,
    });
  } catch (error) {
    if (signal?.aborted) throw error;
    throw new NodeError(0, timeout.aborted ? 'Нода не ответила вовремя.' : unavailable);
  }
  if (!response.ok) {
    const error = await response.json().catch(() => null);
    throw new NodeError(
      response.status,
      error?.message || 'Ошибка запроса к ноде журнала.',
      error?.code,
    );
  }
  if (!response.headers.get('content-type')?.includes('application/json'))
    throw new NodeError(0, unavailable);
  return response.json() as Promise<T>;
}

const measurementLabels: Record<string, string> = {
  coat_light: 'Яркость шерсти',
  coat_warmth: 'Теплота окраса',
  cheek_light: 'Яркость щеки',
  muzzle_contrast: 'Контраст морды',
  head_ratio: 'Пропорции головы',
};
const traitLabels: Record<string, string> = {
  muzzle: 'Морда',
  mane: 'Грива',
  coat: 'Масть',
  head: 'Голова',
  ears: 'Уши',
  markings: 'Отметины',
  scars: 'Шрамы',
  proportions: 'Пропорции',
};
const traitValues: Record<string, string> = {
  light: 'светлая',
  dark: 'тёмная',
  straight: 'прямая',
  gray: 'серая',
  white: 'белая',
  bay: 'гнедая',
  dun: 'саврасая',
  black: 'вороная',
  chestnut: 'рыжая',
  erect: 'стоячие',
  none: 'нет',
};
const measurementFormat = new Intl.NumberFormat('ru', { maximumFractionDigits: 3 });
function annotation(data?: HorseAnnotation | null): Annotation {
  if (!data) return noMarks;
  return {
    outline: data.horse_polygon,
    partMask: data.part_mask,
    traits: data.traits.map(({ key, value, polygon }) => ({
      key,
      label: measurementLabels[key] ?? traitLabels[key] ?? key,
      value:
        measurementLabels[key] && value.trim() && Number.isFinite(Number(value))
          ? measurementFormat.format(Number(value))
          : traitValues[value] ?? value,
      polygon: polygon ?? undefined,
      at: polygon?.[0],
    })),
  };
}
function photo(data: ObservationPhoto, fallback: string): Photo {
  return {
    id: data.photo_id,
    name: data.name || fallback,
    source: root + '/blobs/' + encodeURIComponent(data.blob_sha256),
    angle: data.angle || 'Без ракурса',
    framing: data.framing || 'Без плана',
    quality: data.quality,
    annotation: annotation(data.annotation),
  };
}
function observation(row: NodeObservation, blobs: Map<string, string>): Observation {
  const name = row.name || 'Наблюдение ' + row.id.slice(-8);
  const photos =
    row.photos ??
    (row.blob_ids ?? []).map((id, index) => ({
      photo_id: row.id + '-' + index,
      blob_sha256: blobs.get(id) ?? id,
      annotation: null,
    }));
  const images = photos.map((image, i) => photo(image, name + ' · ' + (i + 1)));
  return {
    id: row.id,
    name,
    horseId: row.horse_id || null,
    date: row.observed_at,
    comment: row.comment,
    coordinates: row.location,
    observer: row.observer_id,
    photos: images,
    traits: images.flatMap((image) => image.annotation.traits),
  };
}
export async function getNodeBook(signal?: AbortSignal): Promise<Notebook> {
  const node = await request<NodeInfo>('/node', 'GET', undefined, signal).catch((error) => {
    if (signal?.aborted) throw error;
    throw new Error(unavailable);
  });
  if (!node?.node_id || !node.driver) throw new Error(unavailable);
  const [horses, observations, blobs] = await Promise.all([
    request<{ id: string; metadata?: { name?: string; nickname?: string } }[]>(
      '/horses',
      'GET',
      undefined,
      signal,
    ),
    request<NodeObservation[]>(
      '/observations?include=photos,annotations',
      'GET',
      undefined,
      signal,
    ),
    request<{ id: string; sha256: string; size: number }[]>('/blobs', 'GET', undefined, signal),
  ]);
  const hashes = new Map(blobs.map((b) => [b.id, b.sha256]));
  return {
    node,
    horses: horses.map((h) => ({
      id: h.id,
      name: h.metadata?.name || h.metadata?.nickname || h.id,
    })),
    observations: observations.map((o) => observation(o, hashes)),
    review: null,
    online: true,
    failNextSave: false,
    pending: [],
    sent: 0,
    lastSync: null,
    syncError: null,
    mediaBytes: blobs.reduce((sum, b) => sum + b.size, 0),
  };
}
export async function getHorseHistory(horseId: string, book: Notebook, signal: AbortSignal) {
  const query = new URLSearchParams({ horse_id: horseId, include: 'photos,annotations' });
  const rows = await request<NodeObservation[]>('/observations?' + query, 'GET', undefined, signal);
  return rows.map(
    (row) =>
      (!row.photos && book.observations.find((o) => o.id === row.id)) ||
      observation(row, new Map()),
  );
}
async function upload(file: ImportPhoto) {
  const data = new FormData();
  data.append('file', file.original, file.filename);
  const { sha256 } = await request<{ sha256: string }>('/blobs', 'POST', data);
  if (!/^[a-f0-9]{64}$/.test(sha256)) throw new Error('Нода не вернула хеш снимка.');
  return sha256;
}
export async function analyze(file: ImportPhoto): Promise<Review> {
  const hash = await upload(file);
  const body: ComparisonRequest = { blob_sha256: hash, top_k: 5 };
  let result: ComparisonResult;
  try {
    result = await request<ComparisonResult>('/comparisons', 'POST', body);
  } catch (error) {
    if (
      error instanceof NodeError &&
      (error.code === 'local_inference_unavailable' ||
        error.code === 'local_segmentation_unavailable')
    )
      throw error;
    if (error instanceof NodeError && [404, 405, 501, 503].includes(error.status))
      throw new Error('Сравнение недоступно на этой ноде.');
    throw error;
  }
  if (result.blob_sha256 !== hash || !Array.isArray(result.detections))
    throw new Error('Нода вернула некорректный результат сравнения.');
  return {
    id: result.comparison_id,
    illustrative: false,
    photo: photo({ photo_id: hash, blob_sha256: hash, annotation: null }, file.filename),
    detections: result.detections.map((d, index) => ({
      id: d.detection_id,
      number: index + 1,
      annotation: annotation(d.annotation),
      confidence: d.detection_confidence,
      candidates: d.candidates.map((c) => ({
        horseId: c.horse_id,
        name: c.name,
        score: c.cosine_similarity,
        bestPhotoId: c.best_photo_id,
      })),
    })),
  };
}
export async function decide(input: Decision) {
  const path =
    '/comparisons/' +
    encodeURIComponent(input.reviewId) +
    '/detections/' +
    encodeURIComponent(input.detectionId) +
    '/decision';
  const observation = { observation_name: input.name.trim(), comment: input.comment?.trim() || undefined };
  const body: ComparisonDecision = input.horseId === null
    ? { ...observation, action: 'create_horse', horse_name: input.horseName!.trim() }
    : { ...observation, action: 'match', horse_id: input.horseId };
  return request<DecisionResult>(path, 'PUT', body);
}
export function cancelComparison(id: string) {
  return request<CancelResult>('/comparisons/' + encodeURIComponent(id) + '/cancel', 'POST');
}
export async function saveManual(input: ManualInput) {
  if (!input.observer?.trim()) throw new Error('Введите идентификатор наблюдателя.');
  const hashes = [];
  for (const file of input.files) hashes.push(await upload(file));
  return request<NodeObservation>('/observations', 'POST', {
    observer_id: input.observer.trim(),
    comment: input.comment?.trim() || undefined,
    location: input.coordinates.trim(),
    horse_id: input.horseId ?? undefined,
    blob_ids: hashes,
  });
}

export interface ModelFile {
  id: string;
  filename: string;
  state: 'missing' | 'checking' | 'downloading' | 'ready' | 'error';
  has_source: boolean;
  bundled?: boolean;
  size?: number;
  received: number;
  total: number;
  message?: string;
}
export interface ModelSetup {
  mode: 'local' | 'external' | 'disabled';
  ready: boolean;
  runtime_ready: boolean;
  models: ModelFile[];
}
export async function getModels(signal: AbortSignal) {
  try {
    return await request<ModelSetup>('/models', 'GET', undefined, signal);
  } catch (error) {
    // Earlier nodes can serve the existing HTTP comparison contract.
    if (error instanceof NodeError && error.status === 404) return null;
    throw error;
  }
}
export function updateModel(id: string, action: 'download' | 'cancel', url = '') {
  return request<ModelSetup>('/models/' + encodeURIComponent(id) + '/' + action, 'POST', { url });
}
