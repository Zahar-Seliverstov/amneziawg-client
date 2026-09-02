import type { AmneziaConfig, ConnectionStatus } from './useApi'

type Notify = (text: string, type?: 'error' | 'ok') => void

// Список конфигураций и выбранная из них.
//
// Выбор живёт на стороне службы: к нему цепляется автоподключение при
// следующем запуске, поэтому каждое изменение туда и уезжает. Здесь же он
// только отражается.
export function useConfigs(status: MaybeRefOrGetter<ConnectionStatus | null>, notify: Notify) {
  const api = useApi()

  const configs = ref<AmneziaConfig[]>([])
  const selectedId = ref<string | null>(null)

  // Последнее, что мы отправили службе. Нужно, чтобы не слать один и тот же
  // выбор по кругу: перерисовка списка происходит после каждой операции.
  let storedId = ''

  const selected = computed(
    () => configs.value.find(c => c.id === selectedId.value) || null
  )

  const activeName = computed(() => {
    const s = toValue(status)
    return configs.value.find(c => c.id === s?.config_id)?.name || s?.config_name || ''
  })

  async function load() {
    try {
      const [list, storedFromService] = await Promise.all([
        api.getConfigs(),
        // Со старой службой эндпоинта может не быть, но список конфигураций
        // всё равно должен отрисоваться.
        api.getSelectedConfig().catch(() => '')
      ])

      configs.value = list || []
      storedId = storedFromService

      const active = toValue(status)?.config_id
      const exists = (id?: string | null) => Boolean(id) && configs.value.some(c => c.id === id)

      if (exists(active)) {
        selectedId.value = active!
      } else if (exists(storedFromService)) {
        selectedId.value = storedFromService
      } else if (!exists(selectedId.value)) {
        selectedId.value = configs.value[0]?.id ?? null
      }

      // Подхваченный по умолчанию конфиг тоже запоминаем — иначе
      // автоподключение при следующем запуске не будет знать, к чему цепляться.
      await persist(selectedId.value, true)
    } catch (e: any) {
      notify(e.message)
    }
  }

  // silent — для выбора по умолчанию при загрузке: он делается без ведома
  // пользователя, и ругаться на него всплывающим сообщением незачем.
  async function persist(id: string | null, silent = false) {
    const next = id ?? ''
    if (next === storedId) return

    try {
      await api.setSelectedConfig(next)
      storedId = next
    } catch (e: any) {
      if (!silent) notify(e.message)
    }
  }

  // Выбор конфигурации на живом соединении переключает туннель сразу. Служба
  // сама разбирает текущий и поднимает новый, поэтому отдельного «отключить»
  // здесь нет — иначе между двумя запросами возникала бы щель, в которой
  // маршруты успевали слететь.
  async function select(id: string, switchNow: boolean) {
    if (id === selectedId.value) return

    selectedId.value = id
    await persist(id)

    if (!switchNow) return

    try {
      await api.connect(id)
    } catch (e: any) {
      notify(e.message)
    }
  }

  // Служба сама узнаёт, к чему подключилась: подключиться могли и из трея, и
  // автоподключением при запуске.
  function follow(id: string | undefined) {
    if (!id || !configs.value.some(c => c.id === id)) return
    selectedId.value = id
    storedId = id
  }

  return { configs, selectedId, selected, activeName, load, select, follow }
}
