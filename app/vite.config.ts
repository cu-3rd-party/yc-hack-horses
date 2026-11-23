import { defineConfig } from 'vitest/config';
import { readdirSync } from 'node:fs';
import { dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { viteStaticCopy } from 'vite-plugin-static-copy';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
// MPA (ADR-0015): входы собираются из *.html в корне app (PR-ревью #35) —
// хардкод списка валит весь web-билд при отсутствии любого одного файла.
const appRoot = dirname(fileURLToPath(import.meta.url));
const htmlInputs = Object.fromEntries(
  readdirSync(appRoot)
    .filter((name) => name.endsWith('.html'))
    .map((name) => [name.replace(/\.html$/, ''), name]),
);
// Ожидаемые страницы (маршруты Go-ноды и smoke их знают): отсутствующий файл
// — падение сборки с понятной ошибкой-подсказкой, а не «Could not resolve».
const expectedPages = ['index', 'gallery', 'observations', 'system'];
const missing = expectedPages.filter((page) => !(page in htmlInputs));
if (missing.length)
  throw new Error(
    `Нет html-входов в ${appRoot}: ${missing.join(', ')}. ` +
      `Найдено: ${Object.keys(htmlInputs).join(', ') || '—'}. ` +
      `Маршруты ноды (server.go) и .ci/smoke-web.mjs ждут эти страницы.`,
  );
export default defineConfig({
  base: './',
  build: {
    rollupOptions: {
      input: htmlInputs,
    },
  },
  server: { proxy: { '/api/v1': { target: 'http://127.0.0.1:8080' } } },
  plugins: [
    react(),
    tailwindcss(),
    viteStaticCopy({
      targets: [
        ...['cmaps', 'standard_fonts', 'wasm', 'iccs'].map((dir) => ({
          src: 'node_modules/pdfjs-dist/' + dir,
          dest: 'pdfjs',
        })),
        { src: 'node_modules/pdfjs-dist/LICENSE', dest: 'licenses', rename: 'pdfjs.txt' },
      ],
    }),
  ],
  test: { environment: 'node', include: ['src/**/*.test.ts', 'tests/*.test.ts'] },
});
