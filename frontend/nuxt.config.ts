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
  
  runtimeConfig: {
    public: {
      apiBase: 'http://127.0.0.1:8081/api',
      wsBase: 'ws://127.0.0.1:8081/api/ws'
    }
  },
  
  app: {
    head: {
      title: 'AWG Client - AmneziaWG Web Client',
      meta: [
        { charset: 'utf-8' },
        { name: 'viewport', content: 'width=device-width, initial-scale=1' }
      ]
    }
  },

  css: ['~/assets/css/main.css'],

  compatibilityDate: '2024-01-01'
})
