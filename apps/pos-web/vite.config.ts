import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// In development the Go API runs on :8080 and Vite proxies to it, so the UI always
// talks to its own origin, exactly as in production where Go serves the built UI.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/v1': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080',
    },
  },
});
