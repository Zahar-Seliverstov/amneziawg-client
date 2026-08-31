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

// useApiUrl — адрес эндпоинта API: useApiUrl('/configs').
export function useApiUrl(path: string): string {
  return `${useBackendOrigin()}/api${path}`
}

// useWsUrl — адрес канала обновлений. Схема выводится из адреса страницы:
// со страницы, открытой по https, ws:// браузер не пропустит.
export function useWsUrl(): string {
  return `${useBackendOrigin().replace(/^http/, 'ws')}/api/ws`
}
