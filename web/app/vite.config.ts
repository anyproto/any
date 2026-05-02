import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import path from 'node:path';

// https://vitejs.dev/config/
export default defineConfig({
  // The Go server mounts the SPA at /ui (see internal/server/webapp.go).
  // Use a relative base so built asset URLs work no matter where the
  // SPA is served from — including the Vite dev server at /.
  base: './',
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: {
      // The Go server is loopback-only at 127.0.0.1:7001 (see docs/02-server.md)
      '/v1': {
        target: 'http://127.0.0.1:7001',
        changeOrigin: false,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
    // Hashed asset paths so the Go embed handler can serve them with
    // long Cache-Control headers.
    rollupOptions: {
      output: {
        assetFileNames: 'assets/[name]-[hash][extname]',
        chunkFileNames: 'assets/[name]-[hash].js',
        entryFileNames: 'assets/[name]-[hash].js',
      },
    },
  },
});
