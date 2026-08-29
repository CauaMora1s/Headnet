import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// The control plane the dev server proxies to. Keeping the frontend on the
// same origin as the API in development means cookies, CSRF handling and
// same-origin fetches behave the way they will in production, where the Go
// binary serves both.
const CONTROL_PLANE = process.env.HEADNET_DEV_SERVER ?? 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [svelte(), tailwindcss()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: CONTROL_PLANE, changeOrigin: false },
      '/health': { target: CONTROL_PLANE, changeOrigin: false },
      '/ready': { target: CONTROL_PLANE, changeOrigin: false },
    },
  },
  build: {
    outDir: 'dist',
    // Fail the build rather than shipping something unexpectedly large; the
    // admin UI has no business pulling in megabytes.
    chunkSizeWarningLimit: 600,
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
    globals: false,
  },
});
