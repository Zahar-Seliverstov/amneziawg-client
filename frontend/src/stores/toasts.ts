// Уведомления — одно хранилище на всё приложение: сообщить об ошибке может
// любой компонент, не передавая событие наверх через всю цепочку.

import { ref } from 'vue'
import { errorText } from '../api/client'

export type ToastType = 'error' | 'ok'

export interface Toast {
  id: number
  text: string
  type: ToastType
  count: number
}

const MAX_TOASTS = 3

// Ошибку надо успеть прочитать, подтверждение — только заметить.
const LIFETIME: Record<ToastType, number> = { error: 8000, ok: 3500 }

const toasts = ref<Toast[]>([])
const timers = new Map<number, ReturnType<typeof setTimeout>>()
let lastId = 0

export function dismiss(id: number) {
  clearTimeout(timers.get(id))
  timers.delete(id)
  toasts.value = toasts.value.filter(t => t.id !== id)
}

function arm(t: Toast) {
  clearTimeout(timers.get(t.id))
  timers.set(t.id, setTimeout(() => dismiss(t.id), LIFETIME[t.type]))
}

// Одинаковое сообщение не плодит копии, а увеличивает счётчик: при обрыве
// связи одна и та же ошибка приходит десятками.
export function notify(text: string, type: ToastType = 'error') {
  const same = toasts.value.find(t => t.text === text && t.type === type)
  if (same) {
    same.count++
    arm(same)
    return
  }

  const toast: Toast = { id: ++lastId, text, type, count: 1 }
  toasts.value.push(toast)
  arm(toast)

  while (toasts.value.length > MAX_TOASTS) dismiss(toasts.value[0]!.id)
}

export function notifyError(e: unknown) {
  notify(errorText(e))
}

export function useToasts() {
  return { toasts, notify, dismiss }
}
