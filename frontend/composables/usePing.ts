// Задержка до сервера VPN.
//
// Цель выбирает служба: она знает и активный конфиг, и состояние подключения,
// и какие маршруты поставила, — здесь это пришлось бы угадывать разбором
// конфигурации. Замер идёт, только пока соединение установлено.
const INTERVAL = 10000

export function usePing(isConnected: MaybeRefOrGetter<boolean>) {
  const api = useApi()

  const latency = ref<number | null>(null)
  const loading = ref(false)
  let timer: ReturnType<typeof setInterval> | null = null

  async function measure() {
    if (!toValue(isConnected)) {
      latency.value = null
      return
    }

    loading.value = true

    try {
      const res = await api.ping()
      latency.value = res.success && typeof res.latency === 'number' ? res.latency : null
    } catch (e) {
      console.error('Ping error:', e)
      latency.value = null
    } finally {
      loading.value = false
    }
  }

  function start() {
    stop()
    measure()
    timer = setInterval(measure, INTERVAL)
  }

  function stop() {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
    latency.value = null
    loading.value = false
  }

  // Нажатие на значок меряет сразу и начинает отсчёт заново: иначе очередной
  // автоматический замер мог прийти через долю секунды после ручного и сбить
  // только что показанное число.
  function refresh() {
    if (loading.value) return
    start()
  }

  watch(() => toValue(isConnected), (connected) => {
    if (connected) start()
    else stop()
  }, { immediate: true })

  onScopeDispose(stop)

  // Дробная часть нужна только у совсем малых значений: на 42.3ms десятая
  // доля ничего не решает, а ширина значка от неё скачет.
  const text = computed(() => {
    if (latency.value === null) return loading.value ? '…' : '—'
    const ms = latency.value
    return `${ms < 10 ? ms.toFixed(1) : Math.round(ms)}ms`
  })

  const title = computed(() => {
    if (latency.value === null) return 'Измерить задержку до сервера'
    return 'Сетевая задержка до сервера, в обход туннеля.\nНажмите, чтобы обновить'
  })

  const grade = computed(() => {
    if (latency.value === null) return ''
    if (latency.value < 100) return 'ping-badge--good'
    if (latency.value < 300) return 'ping-badge--medium'
    return 'ping-badge--bad'
  })

  return { text, title, grade, loading, refresh }
}
