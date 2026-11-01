import { LoaderCircle, Power, X } from '@lucide/vue'
import type { Component } from 'vue'
import type { ConnectionStatus } from '../../api/types'

// Как выглядит каждое состояние соединения: цвет точки, подпись и что
// сделает главная кнопка. Одна таблица на всё, чтобы точка, текст и кнопка
// не разошлись между собой.

export type View = ConnectionStatus['state'] | 'offline'
export type Tone = 'off' | 'busy' | 'on' | 'bad'

export interface StateLook {
  tone: Tone
  label: string
  action: string
  // primary — кнопка зовёт подключиться (главная, контрастная); иначе она
  // вторичная: отключить или прервать.
  primary: boolean
  // icon — значок кнопки: питание — включить или выключить, крестик —
  // прервать попытку, кольцо — служба уже выполняет действие.
  icon: Component
  // busy — значок крутится.
  busy?: boolean
}

export const LOOKS: Record<View, StateLook> = {
  disconnected: { tone: 'off', label: 'Отключено', action: 'Подключить', primary: true, icon: Power },
  connecting: { tone: 'busy', label: 'Подключение…', action: 'Отменить', primary: false, icon: X },
  connected: { tone: 'on', label: 'Подключено', action: 'Отключить', primary: false, icon: Power },
  reconnecting: { tone: 'busy', label: 'Переподключение…', action: 'Остановить', primary: false, icon: X },
  disconnecting: { tone: 'busy', label: 'Отключение…', action: 'Отключение…', primary: false, icon: LoaderCircle, busy: true },
  error: { tone: 'bad', label: 'Не удалось подключиться', action: 'Подключить', primary: true, icon: Power },
  offline: { tone: 'bad', label: 'Нет связи со службой', action: 'Подключить', primary: true, icon: Power }
}
