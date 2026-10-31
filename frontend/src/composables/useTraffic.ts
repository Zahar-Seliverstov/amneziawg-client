import { computed, ref, toValue, watch, type MaybeRefOrGetter } from 'vue'
import type { ConnectionStatus } from '../api/types'

// Объём и скорость трафика туннеля.
//
// Служба присылает счётчики раз в несколько секунд вместе со статусом.
// Скорость считается здесь, по разнице двух соседних замеров: отдельного
// запроса ради неё не нужно, а точнее, чем раз в несколько секунд, она
// человеку и не нужна.
const MIN_WINDOW_MS = 1000

export function useTraffic(source: MaybeRefOrGetter<ConnectionStatus | null>) {
  const rxRate = ref(0)
  const txRate = ref(0)

  let last: { rx: number, tx: number, at: number } | null = null

  watch(
    () => {
      const s = toValue(source)
      return s?.state === 'connected' ? [s.bytes_received, s.bytes_sent] as const : null
    },
    (counters) => {
      if (!counters) {
        last = null
        rxRate.value = 0
        txRate.value = 0
        return
      }

      const [rx, tx] = counters
      const now = Date.now()

      // Счётчики сбрасываются при переподключении — отрицательной скорости
      // не бывает, это просто новый отсчёт.
      // Скорость считаем по отрезку не короче секунды: события приходят и
      // пачками, и деление на десятки миллисекунд давало сотни МБ/с.
      if (last && now - last.at < MIN_WINDOW_MS) return

      if (last && rx >= last.rx && tx >= last.tx) {
        const seconds = (now - last.at) / 1000
        rxRate.value = (rx - last.rx) / seconds
        txRate.value = (tx - last.tx) / seconds
      }
      last = { rx, tx, at: now }
    },
    { immediate: true }
  )

  const rx = computed(() => toValue(source)?.bytes_received ?? 0)
  const tx = computed(() => toValue(source)?.bytes_sent ?? 0)

  return { rx, tx, rxRate, txRate }
}
