import { useEffect, useState } from 'react';
import { readMedia } from './api';
import type { ImportPhoto } from './types';
declare global {
  interface Window {
    journalImages?: { decode: (bytes: Uint8Array) => Promise<Uint8Array<ArrayBuffer>> };
  }
}
export async function importImage(file: File): Promise<ImportPhoto> {
  if (file.type && !file.type.startsWith('image/') && file.type !== 'application/octet-stream')
    throw new Error('Выберите файл изображения.');
  if (!file.size) throw new Error('Файл пуст. Выберите другой снимок.');
  const url = URL.createObjectURL(file);
  try {
    const image = new Image();
    image.src = url;
    await image.decode();
    return { filename: file.name, original: file, preview: file };
  } catch {
    try {
      if (!window.journalImages) throw new Error();
      const data = await window.journalImages.decode(new Uint8Array(await file.arrayBuffer()));
      return {
        filename: file.name,
        original: file,
        preview: new Blob([data], { type: 'image/png' }),
      };
    } catch {
      throw new Error('Не удалось прочитать изображение. Выберите другой файл.');
    }
  } finally {
    URL.revokeObjectURL(url);
  }
}
// Decode originals the browser cannot display using the same native decoder as imports.
async function nodePreview(source: string, signal: AbortSignal) {
  if (!window.journalImages) return;
  const response = await fetch(source, { signal });
  if (!response.ok) return;
  const png = await window.journalImages.decode(new Uint8Array(await response.arrayBuffer()));
  return new Blob([png], { type: 'image/png' });
}

export function usePhotoURL(source: string) {
  const [failedSource, setFailedSource] = useState('');
  const [resolved, setResolved] = useState<{ key: string; url: string }>();
  const stored = source.startsWith('media-');
  const needsPreview = source.startsWith('/api/v1/blobs/') && source === failedSource;
  useEffect(() => {
    if (!stored && !needsPreview) return;
    const controller = new AbortController();
    let url = '';
    const preview = stored
      ? readMedia(source).then((record) => record?.preview)
      : nodePreview(source, controller.signal);
    preview
      .then((blob) => {
        if (blob && !controller.signal.aborted) {
          url = URL.createObjectURL(blob);
          setResolved({ key: source, url });
        }
      })
      .catch(() => {});
    return () => {
      controller.abort();
      if (url) URL.revokeObjectURL(url);
    };
  }, [source, stored, needsPreview]);
  return {
    src: resolved?.key === source ? resolved.url : stored ? undefined : source,
    onError: () => setFailedSource(source),
  };
}
