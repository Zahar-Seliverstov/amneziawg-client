// Типы данных API службы awg-service. Источник правды — internal/api в Go.

// Протокол, который умеет разбирать служба. Интерфейс их не зашивает:
// список приходит от службы (GET /protocols).
export interface Protocol {
  id: string
  title: string
}

// Сохранённая конфигурация в том виде, в каком её отдаёт список. Секретов
// здесь нет: ключи лежат в исходном тексте, а тот приходит только по запросу
// одной конфигурации — форме правки (см. getConfig).
export interface SavedConfig {
  id: string
  name: string
  // protocol — идентификатор протокола; пуст у записей старых версий.
  protocol: string
  created_at: string
}

// Подробности одной конфигурации: то же плюс исходный текст.
export interface SavedConfigDetail extends SavedConfig {
  raw_config: string
}

export interface ConnectionStatus {
  // reconnecting — туннель был поднят и оборвался, идёт восстановление.
  // Отдельное состояние, а не 'connecting': пользователь должен видеть, что
  // связь потеряна, а не что он сам только что нажал кнопку.
  state: 'disconnected' | 'connecting' | 'connected' | 'reconnecting' | 'disconnecting' | 'error'
  config_id?: string
  config_name?: string
  connected_at?: string
  error?: string
  interface?: string
  // attempt — номер идущей попытки подключения; отсутствует, когда соединение
  // установлено или его нет вовсе.
  attempt?: number
  // routing_error — правила не легли на живой туннель. Отдельно от error,
  // потому что это не состояние соединения: туннель поднят и работает, не
  // сложилось только с правилами.
  //
  // Приходит потоком, а не ответом на правку: применение отложено на доли
  // секунды и схлопывает всплеск правок в одну пересборку, поэтому HTTP
  // отвечает раньше, чем маршруты встают.
  routing_error?: string
  bytes_received: number
  bytes_sent: number
  last_handshake?: string
}

export type RoutingMode = 'vpn_list' | 'direct_list'

export type RuleType =
  | 'ip' | 'cidr' | 'domain' | 'zone'
  | 'full' | 'keyword' | 'regexp'
  | 'geoip' | 'geosite'
  | 'app'

export interface RoutingRule {
  id: string
  type: RuleType
  value: string
  enabled: boolean
}

// AppInfo — программа для правила app:: запущенная сейчас или установленная.
export interface AppInfo {
  name: string
  title: string
  icon?: string
  running: number
  installed: boolean
  hint?: string
}

export interface AppCatalog {
  supported: boolean
  apps: AppInfo[]
}

export type GeoKind = 'geoip' | 'geosite'

// GeoTag — категория базы и сколько в ней записей.
export interface GeoTag {
  name: string
  count: number
}

// GeoInfo — состояние одной базы. url — откуда она скачивается.
export interface GeoInfo {
  present: boolean
  size?: number
  updated?: string
  url: string
  updating?: boolean
  error?: string
  tags: GeoTag[]
}

export type GeoState = Record<GeoKind, GeoInfo>

// Отвергнутое правило: значение и причина, которую видно пользователю.
export interface RejectedRule {
  value: string
  reason: string
}

export interface RoutingBatchResult {
  added: RoutingRule[]
  rejected: RejectedRule[]
  removed: number
}

export interface RoutingConfig {
  mode: RoutingMode
  rules: RoutingRule[]
}

export interface AppSettings {
  autoconnect: boolean
}

// PingResult — задержка до сервера VPN, замеренная TCP-соединением
// в обход туннеля.
export interface PingResult {
  success: boolean
  // latency — наименьшее время оборота из серии, в миллисекундах
  latency?: number
  target?: string
  error?: string
}
