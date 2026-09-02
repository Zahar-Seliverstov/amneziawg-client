import type { ConnectionStatus } from './useApi'

// Как состояние соединения выглядит на экране: подписи, цвет кнопки и
// объяснение отказа.
//
// Отдельно от самого состояния (useVpnStatus), потому что вычисления здесь
// чистые: ни таймеров, ни подписок, ни запросов. Поэтому композабл можно
// звать из нескольких мест сразу, ничего при этом не удваивая.

type StatusSource = MaybeRefOrGetter<ConnectionStatus | null>

export function useConnectionView(source: StatusSource, hasSelection: MaybeRefOrGetter<boolean>) {
  const status = computed(() => toValue(source))

  const isConnected = computed(() => status.value?.state === 'connected')
  const isConnecting = computed(() => status.value?.state === 'connecting')
  const isReconnecting = computed(() => status.value?.state === 'reconnecting')

  const killSwitchActive = computed(() => Boolean(status.value?.kill_switch))

  // Соединение «живёт»: либо установлено, либо клиент его сейчас поднимает.
  // Кнопка в этом состоянии отключает, а выбор другой конфигурации
  // переключает туннель, а не начинает подключение с нуля.
  const isLive = computed(
    () => isConnected.value || isConnecting.value || isReconnecting.value
  )

  const powerState = computed(() => {
    const s = status.value?.state
    if (s === 'connected') return 'on'
    if (s === 'connecting' || s === 'disconnecting' || s === 'reconnecting') return 'busy'
    if (s === 'error') return 'error'
    return 'off'
  })

  const statusText = computed(() => {
    switch (status.value?.state) {
      case 'connected': return 'Подключено'
      case 'connecting': return 'Подключение'
      case 'reconnecting': return 'Переподключение'
      case 'disconnecting': return 'Отключение'
      case 'error': return 'Ошибка'
      default: return 'Отключено'
    }
  })

  // Подключение можно прервать: ожидание рукопожатия длится до 45 секунд, и
  // всё это время кнопка была заблокирована. Не даём жать только во время
  // отключения — там уже идёт разбор соединения, и второй запрос ни к чему.
  const canToggle = computed(() => {
    if (status.value?.state === 'disconnecting') return false
    if (isLive.value) return true
    return toValue(hasSelection)
  })

  // Причину показываем и при отказе, и при потере связи. Второе особенно
  // важно: без объяснения «Переподключение» выглядит как зависшая кнопка.
  const connectionError = computed(() => {
    const s = status.value
    if (!s) return null

    if (s.state === 'error') {
      return s.error || 'Не удалось подключиться. Подробности — в системном журнале'
    }

    if (s.state === 'reconnecting') {
      const reason = s.error || 'сервер не отвечает'
      const attempt = s.attempt ? `, попытка ${s.attempt}` : ''
      return `Связь потеряна: ${reason}${attempt}`
    }

    return null
  })

  return {
    isConnected,
    isLive,
    killSwitchActive,
    powerState,
    statusText,
    canToggle,
    connectionError
  }
}
