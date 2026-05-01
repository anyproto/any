/// <reference types="vitest" />
// vitest@2 ships its own vite@5 type copy; @vitejs/plugin-react targets
// vite@6 (our top-level dep). The two PluginOption types disagree
// nominally even though the runtime is fine. The `as any` cast on the
// plugin keeps TypeScript happy without contorting the config.
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import path from 'node:path';

export default defineConfig({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  plugins: [react() as any],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'lcov'],
      exclude: [
        'node_modules/**',
        'dist/**',
        '.storybook/**',
        'e2e/**',
        'src/test/**',
        '**/*.stories.tsx',
        '**/*.test.{ts,tsx}',
      ],
    },
  },
});
