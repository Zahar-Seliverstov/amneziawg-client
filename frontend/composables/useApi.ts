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
  state: 'disconnected' | 'connecting' | 'connected' | 'disconnecting' | 'error'
  config_id?: string
  config_name?: string
  connected_at?: string
  error?: string
  interface?: string
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

export function useApi() {
  async function fetchApi<T>(path: string, options: RequestInit = {}): Promise<T> {
    const res = await fetch(useApiUrl(path), {
      ...options,
      headers: {
        'Content-Type': 'application/json',
        ...options.headers
      }
    })

    // Ответ без тела (или с телом не в JSON) — тоже ответ: разбирать его
    // вслепую значило бы превратить понятную ошибку сервера в невнятное
    // «Unexpected token» из парсера.
    const data = await res.json().catch(() => null)

    if (!res.ok) {
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
    getSelectedConfig,
    setSelectedConfig,
    getSettings,
    setSettings
  }
}
