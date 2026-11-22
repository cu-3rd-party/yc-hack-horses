import { defineConfig } from 'vitest/config';
import { viteStaticCopy } from 'vite-plugin-static-copy';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
export default defineConfig({
  base: './',
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
