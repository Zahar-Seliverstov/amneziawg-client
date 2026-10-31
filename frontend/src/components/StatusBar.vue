<template>
  <section class="bar" :class="`bar--${view}`">
    <div class="cell">
      <span class="caption">Состояние</span>
      <span class="state"><span class="dot" :class="`dot--${view}`" aria-hidden="true"></span>{{ label }}</span>
    </div>

    <div class="cell cell--server">
      <span class="caption">Сервер</span>
      <span class="mono ellipsis" :class="{ faint: !hasServer }" :title="serverName">{{ serverName }}</span>
    </div>

    <template v-if="isConnected">
      <div class="cell" title="Сколько держится соединение">
        <span class="caption">В туннеле</span>
        <span class="mono">{{ duration || '—' }}</span>
      </div>
      <div class="cell">
        <span class="caption">Пинг</span>
        <button class="mono ping" :class="pingClass" :disabled="pingLoading" :title="pingTitle" @click="refreshPing">{{ pingText }}</button>
      </div>
      <div class="cell" title="Принято через туннель">
        <span class="caption">↓ Принято</span>
        <span class="mono">{{ formatBytes(rx) }} <span class="faint">{{ formatRate(rxRate) }}</span></span>
      </div>
      <div class="cell" title="Отправлено через туннель">
        <span class="caption">↑ Отправлено</span>
        <span class="mono">{{ formatBytes(tx) }} <span class="faint">{{ formatRate(txRate) }}</span></span>
      </div>
    </template>

    <button
      class="btn toggle"
      :class="isLive ? '' : 'btn--primary'"
      :disabled="offline || !canToggle"
      title="Клавиша C"
      @click="$emit('toggle')"
    >
      {{ actionText }}
    </button>

    <p v-if="message" class="error">{{ message }}</p>
  </section>
</template>

<script setup lang="ts">
// Строка состояния: что с соединением сейчас и одна главная кнопка.
// Каждое значение подписано — без подписи «6:37» и «190 МБ» не отличить.
import { computed } from 'vue'
import type { AmneziaConfig, ConnectionStatus } from '../api/types'
import { useConnectionDuration } from '../composables/useConnectionDuration'
import { useConnectionView } from '../composables/useConnectionView'
import { usePing } from '../composables/usePing'
import { useTraffic } from '../composables/useTraffic'
import { formatBytes, formatRate } from '../lib/format'

const props = defineProps<{
  status: ConnectionStatus | null
  configsCount: number
  selected: AmneziaConfig | null
  activeName: string
  offline: boolean
}>()

defineEmits<{ toggle: [] }>()

const { isConnected, isLive, powerState, statusText, canToggle, connectionError } =
  useConnectionView(() => props.status, () => Boolean(props.selected))

const duration = useConnectionDuration(() => props.status?.connected_at)
const { rx, tx, rxRate, txRate } = useTraffic(() => props.status)
const { text: pingText, title: pingTitle, grade, loading: pingLoading, refresh: refreshPing } = usePing(isConnected)

const PING_CLASS: Record<string, string> = { good: 'ok', medium: 'warn', bad: 'bad' }
const pingClass = computed(() => PING_CLASS[grade.value] ?? '')

const view = computed(() => (props.offline ? 'offline' : powerState.value))
const label = computed(() => (props.offline ? 'Нет связи со службой' : statusText.value))

const hasServer = computed(() => isLive.value || Boolean(props.selected))

const serverName = computed(() => {
  if (isLive.value) return props.activeName
  if (props.selected) return props.selected.name
  return props.configsCount ? 'не выбран' : 'добавьте подключение'
})

const actionText = computed(() => {
  switch (props.status?.state) {
    case 'connected': return 'Отключить'
    case 'connecting':
    case 'reconnecting': return 'Остановить'
    case 'disconnecting': return 'Отключение…'
    default: return 'Подключить'
  }
})

const message = computed(() => {
  if (props.offline) {
    return 'Служба awg-service не отвечает. Linux: systemctl status awg-service · Windows: services.msc → «AWG Client»'
  }
  return connectionError.value
})
</script>

<style scoped>
.bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px 28px;
  padding: 12px 16px 12px 14px;
  border-bottom: 1px solid var(--line);
  border-left: 3px solid var(--faint);
}

.bar--on { border-left-color: var(--ok); }
.bar--busy { border-left-color: var(--warn); }
.bar--error,
.bar--offline { border-left-color: var(--bad); }

.cell {
  display: grid;
  gap: 1px;
  min-width: 0;
}

.cell--server {
  max-width: 240px;
}

.state {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}

.bar--on .state { color: var(--ok); }
.bar--busy .state { color: var(--warn); }
.bar--error .state,
.bar--offline .state { color: var(--bad); }

.ping {
  padding: 0;
  text-align: left;
}

.ping:hover:not(:disabled) {
  text-decoration: underline;
}

.toggle {
  margin-left: auto;
  min-width: 112px;
}

.error {
  flex-basis: 100%;
  color: var(--bad);
  font-size: 13px;
}
</style>
