import type { ConnectionStatus } from './useApi'

// Переподключение с нарастающей паузой.
//
// Фиксированные три секунды означали стук в backend каждые три секунды без
// конца — а backend может быть не запущен вовсе, и тогда вкладка колотится в
// него часами. Пауза растёт до полуминуты, но после первого же удачного
// соединения сбрасывается: короткий обрыв (перезапуск backend'а) по-прежнему
// восстанавливается почти мгновенно.
const RECONNECT_MIN = 1000
const RECONNECT_MAX = 30000

export function useWebSocket() {
  const status = ref<ConnectionStatus | null>(null)
  const isConnected = ref(false)
  const error = ref<string | null>(null)

  let ws: WebSocket | null = null
  let reconnectTimeout: ReturnType<typeof setTimeout> | null = null
  let reconnectDelay = RECONNECT_MIN
  // closed отличает наш собственный уход со страницы от обрыва связи: без
  // него close() из disconnect() запускал бы переподключение к странице,
  // которой уже нет.
  let closed = false

  function connect() {
    if (ws?.readyState === WebSocket.OPEN || ws?.readyState === WebSocket.CONNECTING) return

    closed = false

    try {
      const socket = new WebSocket(useWsUrl())
      ws = socket

      socket.onopen = () => {
        isConnected.value = true
        error.value = null
        reconnectDelay = RECONNECT_MIN
      }

      socket.onclose = () => {
        isConnected.value = false
        // Событие от предыдущего, уже заменённого сокета игнорируем.
        if (ws === socket) scheduleReconnect()
      }

      socket.onerror = () => {
        // Подробности браузер в событие не кладёт, а само по себе оно ещё не
        // означает разрыва: решение принимает onclose.
        error.value = 'Нет связи со службой'
      }

      socket.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data)
          if (msg.type === 'status') {
            status.value = msg.data
          }
        } catch (e) {
          console.error('Не удалось разобрать сообщение WebSocket:', e)
        }
      }
    } catch (e) {
      error.value = 'Не удалось подключиться к службе'
      scheduleReconnect()
    }
  }

  function scheduleReconnect() {
    if (closed || reconnectTimeout) return

    const delay = reconnectDelay
    reconnectDelay = Math.min(reconnectDelay * 2, RECONNECT_MAX)

    reconnectTimeout = setTimeout(() => {
      reconnectTimeout = null
      connect()
    }, delay)
  }

  function disconnect() {
    closed = true

    if (reconnectTimeout) {
      clearTimeout(reconnectTimeout)
      reconnectTimeout = null
    }

    if (ws) {
      ws.close()
      ws = null
    }
    isConnected.value = false
  }

  onMounted(connect)
  onUnmounted(disconnect)

  return {
    status: readonly(status),
    isConnected: readonly(isConnected),
    error: readonly(error),
    connect,
    disconnect
  }
}
