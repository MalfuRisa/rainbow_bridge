import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { resolve } from 'path'

const basePath = process.env.BASE_PATH || '/rainbow-bridge/'
const normalizedBasePath = basePath.replace(/\/$/, '')
const apiTarget = process.env.RAINBOW_API_TARGET || 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  base: basePath,
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    port: 3000,
    host: true,
    proxy: {
      [`${normalizedBasePath}/api`]: {
        target: apiTarget,
        changeOrigin: true,
        secure: false,
      },
    },
  },
})
