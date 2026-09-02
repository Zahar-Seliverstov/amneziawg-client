import { listen, type UnlistenFn } from '@tauri-apps/api/event'
import type { ConnectionStatus } from './useApi'

// Состояние подключения приходит событиями от оболочки.
//
// Раньше здесь был WebSocket со своим переподключением: страницу отдавал
// backend, и следить за связью приходилось ей самой. Теперь поток статуса
// читает оболочка по unix-сокету (desktop/src-tauri/src/events.rs) — она же
// и восстанавливает его после обрыва, потому что следить за VPN нужно и
// когда окно спрятано в трей.
export function useVpnStatus() {
  const status = ref<ConnectionStatus | null>(null)
  const isConnected = ref(false)
  const error = ref<string | null>(null)

  let unlisten: UnlistenFn[] = []

  onMounted(async () => {
    // Текущее состояние спрашиваем сами: события начались раньше, чем
    // смонтировалась страница, и первое из них мы уже не услышим.
    try {
      status.value = await useApi().getStatus()
      isConnected.value = true
    } catch (e) {
      error.value = 'Нет связи со службой'
    }

    unlisten.push(
      await listen<ConnectionStatus>('vpn:status', (event) => {
        status.value = event.payload
        isConnected.value = true
        error.value = null
      })
    )

    unlisten.push(
      await listen('vpn:offline', () => {
        isConnected.value = false
        error.value = 'Нет связи со службой'
      })
    )
  })

  onUnmounted(() => {
    unlisten.forEach(stop => stop())
    unlisten = []
  })

  return {
    status: readonly(status),
    isConnected: readonly(isConnected),
    error: readonly(error)
  }
}
