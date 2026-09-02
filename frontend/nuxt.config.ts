// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  devtools: { enabled: true },
  
  // Отключаем телеметрию
  telemetry: false,
  
  modules: [],

  // Отключает шумные Vite-ошибки "Failed to resolve import #app-manifest" в dev
  experimental: {
    appManifest: false
  },
  
  // Интерфейс живёт внутри десктопной оболочки и работает без сервера:
  // страницы отдаёт сам Tauri из бандла, а данные приходят через его команды.
  ssr: false,

  app: {
    head: {
      title: 'AWG Client',
      meta: [
        { charset: 'utf-8' },
        { name: 'viewport', content: 'width=device-width, initial-scale=1' }
      ],
      link: [
        { rel: 'icon', type: 'image/svg+xml', href: '/logo.svg' }
      ]
    }
  },

  css: ['~/assets/css/main.css'],

  compatibilityDate: '2024-01-01'
})
