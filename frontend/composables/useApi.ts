import { invoke } from '@tauri-apps/api/core'

export interface AmneziaConfig {
  id: string
  name: string
  raw_config: string
  created_at: string
  interface: {
    private_key: string
    address: string[]
    dns?: string[]
    mtu?: number
    jc?: number
    jmin?: number
    jmax?: number
    s1?: number
    s2?: number
    h1?: number
    h2?: number
    h3?: number
    h4?: number
  }
  peers: {
    public_key: string
    preshared_key?: string
    endpoint?: string
    allowed_ips: string[]
    persistent_keepalive?: number
  }[]
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
  // kill_switch — блокировка трафика мимо туннеля сейчас действует
  kill_switch?: boolean
  bytes_received: number
  bytes_sent: number
  last_handshake?: string
}

export type RoutingMode = 'vpn_list' | 'direct_list'

export interface RoutingRule {
  id: string
  type: 'ip' | 'cidr' | 'domain' | 'zone'
  value: string
  enabled: boolean
}

export interface RoutingConfig {
  mode: RoutingMode
  rules: RoutingRule[]
}

export interface AppSettings {
  autoconnect: boolean
  // kill_switch переключается своим эндпоинтом (setKillSwitch): у блокировки
  // есть доступность, и включать её нужно вместе с применением на живом
  // туннеле. Здесь поле только читается.
  kill_switch?: boolean
}

// KillSwitchState — блокировка трафика мимо туннеля.
export interface KillSwitchState {
  // enabled — настройка включена пользователем
  enabled: boolean
  // available — блокировку вообще можно включить на этой машине
  available: boolean
  // active — блокировка сейчас действительно стоит
  active: boolean
  // reason — почему недоступна либо почему не действует при включённой настройке
  reason?: string
}

export interface AutostartState {
  // enabled — ярлык автозапуска создан
  enabled: boolean
  // available — автозапуск вообще можно переключать на этой машине
  available: boolean
  // reason — почему нельзя, если available === false
  reason?: string
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

// Ответ службы в том виде, в каком его отдаёт оболочка.
interface ApiResponse {
  status: number
  body: string
}

interface ApiRequest {
  method?: string
  body?: string
}

function parseBody(body: string): any {
  if (!body.trim()) return null

  try {
    return JSON.parse(body)
  } catch {
    return null
  }
}

export function useApi() {
  // Запросы идут не через fetch, а через оболочку: служба слушает unix-сокет
  // с правами 0600, и до него из окна не дотянуться — сокеты браузеру
  // недоступны. Заодно отпадает и токен: доступ решают права файла.
  async function fetchApi<T>(path: string, options: ApiRequest = {}): Promise<T> {
    let res: ApiResponse

    try {
      res = await invoke<ApiResponse>('api_request', {
        method: options.method || 'GET',
        path: `/api${path}`,
        body: options.body ?? null
      })
    } catch (e) {
      // Сюда попадаем, только если не удалось дойти до самой службы.
      throw new Error(`Нет связи со службой: ${e}`)
    }

    // Ответ без тела (или с телом не в JSON) — тоже ответ: разбирать его
    // вслепую значило бы превратить понятную ошибку службы в невнятное
    // «Unexpected token» из парсера.
    const data = parseBody(res.body)

    if (res.status < 200 || res.status >= 300) {
      throw new Error(data?.error || `Ошибка ${res.status}`)
    }

    return data as T
  }
  
  // Configs
  async function getConfigs(): Promise<AmneziaConfig[]> {
    return fetchApi<AmneziaConfig[]>('/configs')
  }
  
  async function addConfig(name: string, rawConfig: string): Promise<AmneziaConfig> {
    return fetchApi<AmneziaConfig>('/configs', {
      method: 'POST',
      body: JSON.stringify({ name, raw_config: rawConfig })
    })
  }
  
  async function updateConfig(id: string, name: string, rawConfig: string): Promise<AmneziaConfig> {
    return fetchApi<AmneziaConfig>(`/configs/${id}`, {
      method: 'PUT',
      body: JSON.stringify({ name, raw_config: rawConfig })
    })
  }

  async function deleteConfig(id: string): Promise<void> {
    await fetchApi(`/configs/${id}`, { method: 'DELETE' })
  }
  
  // VPN
  async function getStatus(): Promise<ConnectionStatus> {
    return fetchApi<ConnectionStatus>('/vpn/status')
  }
  
  async function connect(configId: string): Promise<void> {
    await fetchApi('/vpn/connect', {
      method: 'POST',
      body: JSON.stringify({ config_id: configId })
    })
  }
  
  async function disconnect(): Promise<void> {
    await fetchApi('/vpn/disconnect', { method: 'POST' })
  }
  
  // Routing
  async function getRouting(): Promise<RoutingConfig> {
    return fetchApi<RoutingConfig>('/routing')
  }
  
  async function setRoutingMode(mode: RoutingMode): Promise<void> {
    await fetchApi('/routing/mode', {
      method: 'PUT',
      body: JSON.stringify({ mode })
    })
  }
  
  async function setRouting(routing: RoutingConfig): Promise<RoutingConfig> {
    return fetchApi<RoutingConfig>('/routing', {
      method: 'PUT',
      body: JSON.stringify(routing)
    })
  }

  async function addRoutingRule(type: string, value: string): Promise<RoutingRule> {
    return fetchApi<RoutingRule>('/routing/rules', {
      method: 'POST',
      body: JSON.stringify({ type, value, enabled: true })
    })
  }
  
  async function deleteRoutingRule(id: string): Promise<void> {
    await fetchApi(`/routing/rules/${id}`, { method: 'DELETE' })
  }
  
  // Автозапуск десктопной оболочки при входе в систему
  async function getAutostart(): Promise<AutostartState> {
    return fetchApi<AutostartState>('/autostart')
  }
  
  async function setAutostart(enabled: boolean): Promise<AutostartState> {
    return fetchApi<AutostartState>('/autostart', {
      method: 'PUT',
      body: JSON.stringify({ enabled })
    })
  }
  
  // Блокировка трафика мимо туннеля
  async function getKillSwitch(): Promise<KillSwitchState> {
    return fetchApi<KillSwitchState>('/kill-switch')
  }

  async function setKillSwitch(enabled: boolean): Promise<KillSwitchState> {
    return fetchApi<KillSwitchState>('/kill-switch', {
      method: 'PUT',
      body: JSON.stringify({ enabled })
    })
  }

  // Выбранный на главном экране конфиг (к нему идёт автоподключение)
  async function getSelectedConfig(): Promise<string> {
    const res = await fetchApi<{ config_id: string }>('/selected-config')
    return res.config_id || ''
  }
  
  async function setSelectedConfig(configId: string): Promise<void> {
    await fetchApi('/selected-config', {
      method: 'PUT',
      body: JSON.stringify({ config_id: configId })
    })
  }
  
  // Settings
  async function getSettings(): Promise<AppSettings> {
    return fetchApi<AppSettings>('/settings')
  }
  
  async function setSettings(settings: AppSettings): Promise<AppSettings> {
    return fetchApi<AppSettings>('/settings', {
      method: 'PUT',
      body: JSON.stringify(settings)
    })
  }
  
  async function ping(): Promise<PingResult> {
    return fetchApi<PingResult>('/ping')
  }

  // Версию показывает backend: она подставляется ему на сборке из манифеста
  // Tauri. Своей копии числа у интерфейса нет намеренно — вписанное руками
  // «1.0.0» пережило выпуск 1.1.0 и врало пользователю.
  async function getVersion(): Promise<string> {
    const res = await fetchApi<{ version: string }>('/version')
    return res.version || ''
  }
  
  return {
    ping,
    getVersion,
    getConfigs,
    addConfig,
    updateConfig,
    deleteConfig,
    getStatus,
    connect,
    disconnect,
    getRouting,
    setRoutingMode,
    setRouting,
    addRoutingRule,
    deleteRoutingRule,
    getAutostart,
    setAutostart,
    getKillSwitch,
    setKillSwitch,
    getSelectedConfig,
    setSelectedConfig,
    getSettings,
    setSettings
  }
}
