import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
export default defineConfig({
  base: './',
  server: { proxy: { '/api/v1': { target: 'http://127.0.0.1:8080' } } },
  plugins: [react(), tailwindcss()],
  test: { environment: 'node', include: ['src/**/*.test.ts'] },
});
