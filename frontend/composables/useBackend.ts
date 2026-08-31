// Адрес backend'а — единственное место, где он вычисляется.
//
// Раньше порт был вписан числом прямо в useApi и useWebSocket. Это ломалось
// молча: порт задаётся флагом при запуске backend'а (-port), и стоило его
// поменять — интерфейс переставал видеть API, показывая пустой экран без
// единого намёка на причину. Ещё и в двух местах сразу, с риском разъехаться
// между собой.
//
// В рабочем режиме страницу отдаёт сам backend — и десктопной оболочке, и
// браузеру. Значит, его адрес это адрес самой страницы, каким бы ни был порт.
//
// Отдельно задавать адрес приходится только в разработке: там страницу отдаёт
// сервер Nuxt на своём порту. Значение берётся из runtimeConfig и
// переопределяется переменной окружения NUXT_PUBLIC_BACKEND_ORIGIN.
export function useBackendOrigin(): string {
  const config = useRuntimeConfig()

  if (import.meta.client && !import.meta.dev) {
    return window.location.origin
  }

  return config.public.backendOrigin
}

// Токен доступа к API.
//
// В рабочем режиме он не нужен: страницу отдаёт сам backend, и при первом
// открытии по ссылке с токеном браузер получает cookie, которую дальше носит
// сам — в том числе на рукопожатие WebSocket.
//
// В разработке страницу отдаёт сервер Nuxt со своего порта, cookie ему никто
// не выдавал, и токен приходится передавать явно. Значение подставляет
// start.sh через NUXT_PUBLIC_BACKEND_TOKEN.
export function useBackendToken(): string {
  return useRuntimeConfig().public.backendToken || ''
}

// useApiUrl — адрес эндпоинта API: useApiUrl('/configs').
export function useApiUrl(path: string): string {
  return `${useBackendOrigin()}/api${path}`
}

// useApiHeaders — заголовки запроса к API.
export function useApiHeaders(): Record<string, string> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }

  const token = useBackendToken()
  if (token) headers.Authorization = `Bearer ${token}`

  return headers
}

// useWsUrl — адрес канала обновлений. Схема выводится из адреса страницы:
// со страницы, открытой по https, ws:// браузер не пропустит.
//
// Токен уходит параметром адреса: заголовки в браузерном API WebSocket
// задать нечем, а cookie в разработке нет.
export function useWsUrl(): string {
  const base = `${useBackendOrigin().replace(/^http/, 'ws')}/api/ws`
  const token = useBackendToken()

  return token ? `${base}?token=${encodeURIComponent(token)}` : base
}
