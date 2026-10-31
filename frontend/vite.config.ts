import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// Интерфейс — статические файлы: их раздаёт awg-ui, он же пересылает /api
// службе. В разработке (npm run dev) /api уходит прямо в службу; служба
// пускает только петлевой Host, поэтому changeOrigin.
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': { target: 'http://127.0.0.1:7071', changeOrigin: true }
    }
  },
  build: {
    target: 'es2022',
    reportCompressedSize: false
  }
})
