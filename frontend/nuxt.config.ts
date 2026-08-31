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
  
  // Адрес backend'а нужен только в разработке: собранный интерфейс раздаёт
  // сам backend, и там его адрес совпадает с адресом страницы (см.
  // composables/useBackend.ts). Переопределяется через NUXT_PUBLIC_BACKEND_ORIGIN.
  runtimeConfig: {
    public: {
      backendOrigin: 'http://127.0.0.1:8081'
    }
  },
  
  app: {
    head: {
      title: 'AWG Client - AmneziaWG Web Client',
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
