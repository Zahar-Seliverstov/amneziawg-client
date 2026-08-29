// Всплывающие сообщения. Одинаковые не плодятся, а слипаются в одну карточку
// со счётчиком: иначе повторяющееся действие — сохранение настроек, ошибка
// подряд — забивает экран одинаковыми сообщениями.
export interface Toast {
  id: number
  text: string
  type: 'error' | 'ok'
  count: number
}

// Больше трёх на экране не держим: старые вытесняются новыми.
const MAX_TOASTS = 3

const LIFETIME = {
  error: 8000,
  ok: 3500
}

export function useToasts(defaultType: Toast['type'] = 'error') {
  const toasts = ref<Toast[]>([])
  const timers = new Map<number, ReturnType<typeof setTimeout>>()
  let toastId = 0

  function dismiss(id: number) {
    const timer = timers.get(id)
    if (timer) {
      clearTimeout(timer)
      timers.delete(id)
    }
    toasts.value = toasts.value.filter(t => t.id !== id)
  }

  // Повторное сообщение продлевает жизнь карточке, а не создаёт новую.
  function armTimer(t: Toast) {
    const prev = timers.get(t.id)
    if (prev) clearTimeout(prev)
    timers.set(t.id, setTimeout(() => dismiss(t.id), LIFETIME[t.type]))
  }

  function notify(text: string, type: Toast['type'] = defaultType) {
    const same = toasts.value.find(t => t.text === text && t.type === type)
    if (same) {
      same.count++
      armTimer(same)
      return
    }

    const toast: Toast = { id: ++toastId, text, type, count: 1 }
    toasts.value.push(toast)
    armTimer(toast)

    while (toasts.value.length > MAX_TOASTS) {
      dismiss(toasts.value[0]!.id)
    }
  }

  // Таймеры переживают уход со страницы, поэтому гасим их вместе с компонентом.
  onScopeDispose(() => {
    timers.forEach(clearTimeout)
    timers.clear()
  })

  return { toasts, notify, dismiss }
}
