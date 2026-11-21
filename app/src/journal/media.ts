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
// Object URLs live only while the component displays that particular photo.
export function usePhotoURL(source: string) {
  const [resolved, setResolved] = useState<{ key: string; url: string }>();
  useEffect(() => {
    if (!source.startsWith('media-')) return;
    let alive = true,
      url = '';
    readMedia(source)
      .then((record) => {
        if (record && alive) {
          url = URL.createObjectURL(record.preview);
          setResolved({ key: source, url });
        }
      })
      .catch(() => {});
    return () => {
      alive = false;
      if (url) URL.revokeObjectURL(url);
    };
  }, [source]);
  return source.startsWith('media-')
    ? resolved?.key === source
      ? resolved.url
      : undefined
    : source;
}
