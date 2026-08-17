import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    host: '0.0.0.0',
    port: 5173,
    proxy: {
      '/console/v1': 'http://127.0.0.1:18081',
      '/api/v1': 'http://127.0.0.1:18081',
      '/public/v1': 'http://127.0.0.1:18081',
      '/health': 'http://127.0.0.1:18081',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
