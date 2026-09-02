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
    if (!at) return ''

    const diff = Math.max(0, Math.floor((now.value - new Date(at).getTime()) / 1000))
    const h = Math.floor(diff / 3600)
    const m = Math.floor((diff % 3600) / 60)
    const sec = diff % 60
    const pad = (n: number) => String(n).padStart(2, '0')

    return h > 0 ? `${h}:${pad(m)}:${pad(sec)}` : `${m}:${pad(sec)}`
  })
}
