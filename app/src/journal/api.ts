import axios, { AxiosError, type AxiosAdapter } from 'axios';
import { openDB, type DBSchema } from 'idb';
import { initialBook, pairMarks, samplePhoto, singleMark, uploadedPhoto } from './fixtures';
import {
  validName,
  type AnalysisInput,
  type Decision,
  type ImportPhoto,
  type ManualInput,
  type Notebook,
  type Photo,
} from './types';

interface Archive extends DBSchema {
  notebook: { key: string; value: Notebook };
  media: { key: string; value: ImportPhoto };
}
let connection: ReturnType<typeof openDB<Archive>> | undefined;
function database() {
  return (connection ??= openDB<Archive>('takhi-field-journal', 1, {
    upgrade(db) {
      db.createObjectStore('notebook');
      db.createObjectStore('media');
    },
  }));
}
export async function readMedia(id: string) {
  return (await database()).get('media', id);
}
const delay = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
const uid = () => crypto.randomUUID();
type Preferences = Partial<Pick<Notebook, 'online' | 'failNextSave'>>;
interface Commands {
  '/analysis': AnalysisInput;
  '/decisions': Decision;
  '/manual': ManualInput;
  '/preferences': Preferences;
  '/sync': undefined;
  '/reset': undefined;
}
function checkName(name: string) {
  if (!validName(name)) throw new Error('Введите допустимое название файла.');
}
async function localRequest(route: string, payload: unknown): Promise<Notebook> {
  const db = await database();
  // One transaction commits media, observations, queue entries and the remaining detections together.
  const tx = db.transaction(['notebook', 'media'], 'readwrite');
  const books = tx.objectStore('notebook'),
    media = tx.objectStore('media');
  let book = (await books.get('current')) ?? initialBook();
  let feedback = '';
  async function attach(file: ImportPhoto) {
    const key = 'media-' + uid();
    await media.add(file, key);
    book.mediaBytes =
      (book.mediaBytes ?? 0) +
      file.original.size +
      (file.preview === file.original ? 0 : file.preview.size);
    return uploadedPhoto(key, file.filename);
  }
  try {
    if ((route === '/decisions' || route === '/manual') && book.failNextSave) {
      book.failNextSave = false;
      feedback = 'Демонстрационная ошибка сохранения. Повторите попытку.';
    } else
      switch (route) {
        case '/book':
          break;
        case '/analysis': {
          if (book.review) throw new Error('Сначала завершите текущее наблюдение.');
          const input = payload as AnalysisInput;
          const group = 'sample' in input && input.sample === 'pair';
          const photo = 'sample' in input ? samplePhoto(input.sample) : await attach(input.upload);
          const marks = group ? pairMarks : [singleMark];
          book.review = {
            id: uid(),
            photo,
            illustrative: 'upload' in input,
            detections: marks.map((annotation, index) => {
              const ranking =
                index === 0
                  ? ['saran', 'altai', 'luna', 'buran']
                  : ['buran', 'luna', 'saran', 'altai'];
              return {
                id: uid(),
                number: index + 1,
                annotation,
                candidates: ranking.map((horseId, i) => ({
                  horseId,
                  score: ([96, 83, 68, 42][i] ?? 40) - index * 2,
                })),
              };
            }),
          };
          break;
        }
        case '/decisions': {
          const input = payload as Decision;
          checkName(input.name);
          const review = book.review;
          const target = review?.detections.find((d) => d.id === input.detectionId);
          if (!review || review.id !== input.reviewId || !target)
            throw new Error('Это наблюдение уже обработано.');
          let horseId = input.horseId;
          if (horseId === null) {
            horseId = uid();
            book.horses.push({
              id: horseId,
              name: 'Особь ' + String(book.horses.length + 1).padStart(3, '0'),
            });
          } else if (!target.candidates.some((c) => c.horseId === horseId))
            throw new Error('Кандидат недоступен.');
          const photo = {
            ...review.photo,
            id: uid(),
            name: input.name.trim(),
            annotation: target.annotation,
          };
          const id = uid();
          // Append so an existing horse keeps its original reference photo.
          book.observations.push({
            id,
            horseId,
            name: photo.name,
            date: new Date().toISOString(),
            photos: [photo],
            coordinates: '',
            traits: target.annotation.traits.map(({ label, value }) => ({ label, value })),
          });
          book.pending.push(id);
          review.detections = review.detections.filter((d) => d.id !== target.id);
          if (!review.detections.length) book.review = null;
          break;
        }
        case '/manual': {
          const input = payload as ManualInput;
          checkName(input.name);
          if (!input.files.length && !input.traits.length)
            throw new Error('Добавьте фотографию или опишите признаки.');
          if (input.horseId && !book.horses.some((h) => h.id === input.horseId))
            throw new Error('Особь недоступна.');
          if (!Number.isFinite(input.confidence) || input.confidence < 0 || input.confidence > 1)
            throw new Error('Укажите достоверность от 0 до 1.');
          if (
            input.traits.some(
              (t) =>
                !t.label.trim() ||
                !t.value.trim() ||
                (t.confidence !== undefined &&
                  (!Number.isFinite(t.confidence) || t.confidence < 0 || t.confidence > 1)),
            )
          )
            throw new Error('Проверьте значения признаков.');
          if (input.coordinates && !validCoordinates(input.coordinates))
            throw new Error('Проверьте широту и долготу.');
          const photos: Photo[] = [];
          for (const file of input.files) photos.push(await attach(file));
          const id = uid();
          book.observations.push({
            id,
            name: input.name.trim(),
            horseId: input.horseId,
            coordinates: input.coordinates,
            traits: input.traits,
            confidence: input.confidence,
            photos,
            date: new Date().toISOString(),
          });
          book.pending.push(id);
          break;
        }
        case '/preferences':
          Object.assign(book, payload as Preferences);
          break;
        case '/sync':
          if (!book.online) {
            book.syncError = 'Демо-узел недоступен. Очередь сохранена.';
            feedback = book.syncError;
          } else {
            book.sent += book.pending.length;
            book.pending = [];
            book.lastSync = new Date().toISOString();
            book.syncError = null;
          }
          break;
        case '/reset':
          await media.clear();
          book = initialBook();
          break;
        default:
          throw new Error('Неизвестная операция');
      }
    await books.put(book, 'current');
    await tx.done;
  } catch (error) {
    try {
      tx.abort();
    } catch {
      /* Transaction already finished. */
    }
    await tx.done.catch(() => {});
    throw error;
  }
  if (feedback) throw new Error(feedback);
  return book;
}
export function validCoordinates(value: string) {
  const match = /^\s*(-?\d+(?:\.\d+)?)\s*,\s*(-?\d+(?:\.\d+)?)\s*$/.exec(value);
  return !!match && Math.abs(Number(match[1])) <= 90 && Math.abs(Number(match[2])) <= 180;
}
const adapter: AxiosAdapter = async (config) => {
  const url = config.url ?? '';
  if (url !== '/book')
    await delay(
      import.meta.env.MODE === 'test'
        ? 0
        : url === '/analysis'
          ? 1050
          : url === '/preferences'
            ? 80
            : 520,
    );
  try {
    const data = await localRequest(url, config.data);
    return { data, status: 200, statusText: 'OK', headers: {}, config };
  } catch (error) {
    throw new AxiosError(
      error instanceof Error ? error.message : 'Не удалось открыть журнал',
      'MOCK',
      config,
    );
  }
};
const client = axios.create({
  adapter,
  transformRequest: [(value) => value],
  transformResponse: [(value) => value],
});
export async function getBook() {
  return (await client.get<Notebook>('/book')).data;
}
export async function command<K extends keyof Commands>(route: K, input: Commands[K]) {
  return (await client.post<Notebook>(route, input)).data;
}
