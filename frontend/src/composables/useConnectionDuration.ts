import { formatDuration } from '../lib/format'
import { computed, onScopeDispose, ref, toValue, watch, type MaybeRefOrGetter } from 'vue'
// Сколько времени держится соединение.
//
// Счётчик идёт только пока есть с чего считать: таймер, тикающий при
// отключённом VPN, будит окно раз в секунду просто так.
export function useConnectionDuration(connectedAt: MaybeRefOrGetter<string | undefined>) {
  const now = ref(Date.now())
  let ticker: ReturnType<typeof setInterval> | null = null

  function stop() {
    if (ticker) {
      clearInterval(ticker)
      ticker = null
    }
  }

  watch(
    () => toValue(connectedAt),
    (at) => {
      stop()
      if (!at) return

      now.value = Date.now()
      ticker = setInterval(() => { now.value = Date.now() }, 1000)
    },
    { immediate: true }
  )

  onScopeDispose(stop)

  return computed(() => {
    const at = toValue(connectedAt)
    return at ? formatDuration((now.value - new Date(at).getTime()) / 1000) : ''
  })
}
