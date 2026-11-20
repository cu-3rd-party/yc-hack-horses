// Каталог данных приложения для Go Core (SQLite + blobs). Должен быть
// постоянным и недоступным другим приложениям; Paths.document подходит.
// SDK 58: только новый API expo-file-system (Paths/File), legacy-функции удалены.
import { Paths } from 'expo-file-system';

export function appDataDir(): string {
  const uri = Paths.document.uri;
  if (!uri) {
    throw new Error('Paths.document недоступен: Expo FileSystem не смонтирован');
  }
  // Go (sqlite/ФС) не понимает file://-URI — отрезаем схему.
  return uri.replace(/^file:\/\//, '').replace(/\/$/, '');
}
