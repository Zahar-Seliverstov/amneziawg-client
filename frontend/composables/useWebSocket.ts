import type { ConnectionStatus } from './useApi'

export function useWebSocket() {
  const config = useRuntimeConfig()
  
  // Используем тот же хост, с которого открыта страница
  const getWsBase = () => {
    if (import.meta.client) {
      const host = window.location.hostname
      return `ws://${host}:8081/api/ws`
    }
    return config.public.wsBase
  }
  
  const status = ref<ConnectionStatus | null>(null)
  const isConnected = ref(false)
  const error = ref<string | null>(null)
  
  let ws: WebSocket | null = null
  let reconnectTimeout: ReturnType<typeof setTimeout> | null = null
  
  function connect() {
    if (ws?.readyState === WebSocket.OPEN) return
    
    try {
      ws = new WebSocket(getWsBase())
      
      ws.onopen = () => {
        isConnected.value = true
        error.value = null
      }
      
      ws.onclose = () => {
        isConnected.value = false
        scheduleReconnect()
      }
      
      ws.onerror = (e) => {
        error.value = 'WebSocket error'
        console.error('WebSocket error:', e)
      }
      
      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data)
          if (msg.type === 'status') {
            status.value = msg.data
          }
        } catch (e) {
          console.error('Failed to parse WebSocket message:', e)
        }
      }
    } catch (e) {
      error.value = 'Failed to connect'
      scheduleReconnect()
    }
  }
  
  function scheduleReconnect() {
    if (reconnectTimeout) return
    
    reconnectTimeout = setTimeout(() => {
      reconnectTimeout = null
      connect()
    }, 3000)
  }
  
  function disconnect() {
    if (reconnectTimeout) {
      clearTimeout(reconnectTimeout)
      reconnectTimeout = null
    }
    
    if (ws) {
      ws.close()
      ws = null
    }
  }
  
  // Auto-connect on mount
  onMounted(() => {
    connect()
  })
  
  // Cleanup on unmount
  onUnmounted(() => {
    disconnect()
  })
  
  return {
    status: readonly(status),
    isConnected: readonly(isConnected),
    error: readonly(error),
    connect,
    disconnect
  }
}
