import { onMounted, onUnmounted, readonly, ref } from 'vue'
import type { ConnectionStatus } from '../api/types'

// Пауза перед новой попыткой прочитать поток после обрыва: растёт от
// минимума до максимума, пока служба не ответит.
const RETRY_MIN_MS = 500
const RETRY_MAX_MS = 5000

// Состояние подключения приходит потоком GET /api/vpn/events: по строке JSON
// (NDJSON) на каждое изменение, первой строкой — текущее состояние.
//
// Поток обрывается, когда служба перезапускается или останавливается. Тогда
// страница показывает «нет связи» и переоткрывает его сама, пока служба не
// вернётся: за туннелем следит служба, а страница лишь показывает.
export function useVpnStatus() {
  const status = ref<ConnectionStatus | null>(null)
  const error = ref<string | null>(null)

  let abort: AbortController | null = null
  let stopped = false

  function offline() {
    error.value = 'Нет связи со службой'
  }

  async function readStream(signal: AbortSignal) {
    const res = await fetch('/api/vpn/events', { signal })
    if (!res.ok || !res.body) {
      throw new Error(`Ошибка ${res.status}`)
    }

    const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
    let buffer = ''

    for (;;) {
      const { value, done } = await reader.read()
      if (done) return

      buffer += value
      let newline: number
      while ((newline = buffer.indexOf('\n')) >= 0) {
        const line = buffer.slice(0, newline).trim()
        buffer = buffer.slice(newline + 1)
        if (!line) continue

        try {
          status.value = JSON.parse(line)
          error.value = null
        } catch {
          // Битая строка — не повод рвать поток: следующая её заменит.
        }
      }
    }
  }

  async function follow() {
    let delay = RETRY_MIN_MS

    while (!stopped) {
      abort = new AbortController()
      try {
        await readStream(abort.signal)
        // Поток кончился штатно — служба остановилась. Первую попытку
        // делаем быстро: чаще всего это перезапуск.
        delay = RETRY_MIN_MS
      } catch {
        if (stopped) return
      }

      offline()
      await new Promise(resolve => setTimeout(resolve, delay))
      delay = Math.min(delay * 2, RETRY_MAX_MS)
    }
  }

  onMounted(() => {
    stopped = false
    follow()
  })

  onUnmounted(() => {
    stopped = true
    abort?.abort()
    abort = null
  })

  return {
    status: readonly(status),
    error: readonly(error)
  }
}
