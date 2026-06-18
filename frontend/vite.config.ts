import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 后端默认 :8088，开发期代理 /api 到后端
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5180,
    proxy: {
      '/api': { target: 'http://localhost:8088', changeOrigin: true },
    },
  },
})
