// Клиент HTTP-API службы.
//
// Запросы идут на тот же адрес, с которого открыта страница: awg-ui
// пересылает /api службе. Поэтому ни CORS, ни адреса службы интерфейсу знать
// не нужно. Объект один на всё приложение: состояния у него нет.

import type {
  AmneziaConfig,
  AmneziaConfigDetail,
  AppCatalog,
  AppSettings,
  GeoKind,
  GeoState,
  PingResult,
  RoutingBatchResult,
  RoutingConfig,
  RoutingMode
} from './types'

// Ответ без тела или с телом не в JSON — тоже ответ: разбирать его вслепую
// значило бы превратить понятную ошибку службы в «Unexpected token».
function parseBody(body: string): any {
  if (!body.trim()) return null
  try {
    return JSON.parse(body)
  } catch {
    return null
  }
}

async function send<T>(path: string, init: RequestInit = {}): Promise<T> {
  let res: Response
  let body: string
  try {
    res = await fetch(`/api${path}`, init)
    body = await res.text()
  } catch (e) {
    // Сюда попадаем, только если не ответил даже awg-ui.
    throw new Error(`Нет связи с интерфейсом: ${e}`)
  }

  const data = parseBody(body)
  if (!res.ok) throw new Error(data?.error || `Ошибка ${res.status}`)
  return data as T
}

function get<T>(path: string) {
  return send<T>(path)
}

function json<T>(method: 'POST' | 'PUT', path: string, payload?: unknown) {
  return send<T>(path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload ?? {})
  })
}

export const api = {
  // ── Подключения ──────────────────────────────────────────────────────
  getConfigs: () => get<AmneziaConfig[]>('/configs'),

  // Текст .conf запрашивается отдельно и только когда он нужен: в нём
  // приватный ключ, и держать его в памяти окна незачем.
  getConfig: (id: string) => get<AmneziaConfigDetail>(`/configs/${id}`),

  addConfig: (name: string, rawConfig: string) =>
    json<AmneziaConfig>('POST', '/configs', { name, raw_config: rawConfig }),

  updateConfig: (id: string, name: string, rawConfig: string) =>
    json<AmneziaConfig>('PUT', `/configs/${id}`, { name, raw_config: rawConfig }),

  deleteConfig: (id: string) => send<void>(`/configs/${id}`, { method: 'DELETE' }),

  // Выбранное подключение — к нему идёт автоподключение.
  getSelectedConfig: async () => (await get<{ config_id: string }>('/selected-config')).config_id || '',
  setSelectedConfig: (configId: string) => json<void>('PUT', '/selected-config', { config_id: configId }),

  // ── Туннель ──────────────────────────────────────────────────────────
  connect: (configId: string) => json<void>('POST', '/vpn/connect', { config_id: configId }),
  disconnect: () => json<void>('POST', '/vpn/disconnect'),

  // Задержка до сервера, замеренная в обход туннеля.
  ping: () => get<PingResult>('/ping'),

  // ── Маршрутизация ────────────────────────────────────────────────────
  getRouting: () => get<RoutingConfig>('/routing'),
  setRoutingMode: (mode: RoutingMode) => json<void>('PUT', '/routing/mode', { mode }),

  // Пакетная правка: весь пакет — одно сохранение и одна пересборка
  // маршрутов. Плохие правила отвергаются поштучно списком rejected,
  // остальные при этом взяты.
  batchRules: (add: string[], remove: string[] = []) =>
    json<RoutingBatchResult>('POST', '/routing/rules/batch', { add, remove }),

  // Программы для правил app:. Собирает их служба: она видит все процессы.
  getApps: () => get<AppCatalog>('/apps'),

  // ── Базы geoip/geosite ───────────────────────────────────────────────
  getGeo: () => get<GeoState>('/geo'),

  // Скачивание синхронное: ответ приходит, когда база уже стоит.
  updateGeo: (kind: GeoKind, url = '') => json<GeoState>('POST', `/geo/${kind}/update`, { url }),

  // Файл уходит телом запроса как есть: десятки мегабайт двоичных данных.
  uploadGeo: (kind: GeoKind, file: File) => send<GeoState>(`/geo/${kind}`, { method: 'PUT', body: file }),

  // ── Прочее ───────────────────────────────────────────────────────────
  getSettings: () => get<AppSettings>('/settings'),
  setSettings: (settings: AppSettings) => json<AppSettings>('PUT', '/settings', settings),

  // Версию подставляет служба на сборке; своей копии у интерфейса нет.
  getVersion: async () => (await get<{ version: string }>('/version')).version || ''
}

export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}
