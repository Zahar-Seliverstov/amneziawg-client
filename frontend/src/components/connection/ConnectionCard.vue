<template>
  <section class="conn" aria-label="Соединение">
    <div class="conn__info">
      <p class="conn__state" role="status">
        <span class="dot" :class="`dot--${look.tone}`" aria-hidden="true"></span>
        {{ look.label }}
      </p>
      <div class="conn__meta">
        <ConfigPicker
          :configs="configs"
          :protocols="protocols"
          :selected-id="selectedId"
          :active-name="activeName"
          :status="status"
          @select="id => $emit('select', id)"
          @changed="$emit('changed')"
        />
        <template v-if="isConnected">
          <span class="sep" aria-hidden="true">·</span>
          <span class="num" title="Сколько держится соединение">{{ duration }}</span>
          <span class="sep" aria-hidden="true">·</span>
          <button class="ping num" :disabled="pingLoading" :title="pingTitle" @click="refreshPing">
            {{ pingText === '—' ? 'пинг —' : pingText }}
          </button>
        </template>
      </div>
    </div>

    <!-- Без подключений нажимать нечего: единственное действие тогда —
         «Добавить подключение» в строке под состоянием. -->
    <button
      v-if="configs.length"
      class="btn conn__toggle"
      :class="{ 'btn--primary': look.primary }"
      :disabled="offline || !canToggle"
      :title="`${look.action} · клавиша C`"
      @click="$emit('toggle')"
    >
      <component :is="look.icon" :class="{ spin: look.busy }" aria-hidden="true" />
      <span>{{ look.action }}</span>
    </button>

    <p v-if="isConnected" class="conn__traffic num">
      <span title="Принято через туннель">↓ {{ formatBytes(rx) }}<template v-if="rxRate >= 1024"> · {{ formatRate(rxRate) }}</template></span>
      <span title="Отправлено через туннель">↑ {{ formatBytes(tx) }}<template v-if="txRate >= 1024"> · {{ formatRate(txRate) }}</template></span>
    </p>

    <p v-if="message" class="conn__message" :class="{ error: look.tone === 'bad' }" role="alert">{{ message }}</p>
  </section>
</template>

<script setup lang="ts">
// Блок соединения — главное на экране: состояние словом и цветной точкой,
// выбор подключения, одна кнопка. Счётчики — мелкой строкой ниже, только
// пока туннель поднят.
import { computed } from 'vue'
import type { ConnectionStatus, Protocol, SavedConfig } from '../../api/types'
import { useConnectionDuration } from '../../composables/useConnectionDuration'
import { useConnectionView } from '../../composables/useConnectionView'
import { usePing } from '../../composables/usePing'
import { useTraffic } from '../../composables/useTraffic'
import { formatBytes, formatRate } from '../../lib/format'
import ConfigPicker from '../configs/ConfigPicker.vue'
import { LOOKS, type View } from './states'

const props = defineProps<{
  status: ConnectionStatus | null
  offline: boolean
  configs: SavedConfig[]
  protocols: Protocol[]
  selectedId: string | null
  activeName: string
}>()

defineEmits<{ toggle: []; select: [id: string]; changed: [] }>()

const { isConnected, canToggle, connectionError } =
  useConnectionView(() => props.status, () => Boolean(props.selectedId))

const view = computed<View>(() => (props.offline ? 'offline' : props.status?.state ?? 'disconnected'))
const look = computed(() => LOOKS[view.value])

const duration = useConnectionDuration(() => props.status?.connected_at)
const { rx, tx, rxRate, txRate } = useTraffic(() => props.status)
const { text: pingText, title: pingTitle, loading: pingLoading, refresh: refreshPing } = usePing(isConnected)

const message = computed(() => {
  if (props.offline) {
    return 'Служба awg-service не отвечает. Linux: systemctl status awg-service · Windows: services.msc → «AWG Client»'
  }
  return connectionError.value
})
</script>

<style scoped>
.conn {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  grid-template-areas:
    'info    toggle'
    'traffic traffic'
    'msg     msg';
  align-items: center;
  gap: 10px 24px;
}

.conn__info {
  grid-area: info;
  min-width: 0;
  display: grid;
  gap: 2px;
}

.conn__state {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 22px;
  font-weight: 600;
  line-height: 1.3;
}

.dot {
  width: 8px;
  height: 8px;
  flex: none;
  border-radius: 50%;
  background: var(--dot-off);
  transition: background-color var(--t);
}

.dot--on { background: var(--dot-on); }
.dot--busy { background: var(--dot-busy); animation: breathe 1.4s ease-in-out infinite; }
.dot--bad { background: var(--dot-bad); }

@keyframes breathe {
  50% { opacity: 0.35; }
}

.conn__meta {
  display: flex;
  align-items: center;
  white-space: nowrap;
  gap: 0 6px;
  min-width: 0;
  color: var(--muted);
  font-size: 14px;
}

/* Длинное имя подключения сжимается с многоточием, а время и пинг —
   никогда: они короткие и меняются на глазах. */
.conn__meta > :not(.picker) {
  flex: none;
}

.sep {
  color: var(--faint);
}

.ping {
  border-radius: 4px;
  color: inherit;
}

.ping:hover:not(:disabled) {
  color: var(--text);
}

.ping:disabled {
  cursor: progress;
}

.conn__toggle {
  grid-area: toggle;
  min-width: 132px;
  gap: 8px;
  padding: 0 18px 0 15px;
  border-radius: 999px;
  /* Строка ровно по высоте шрифта: с высотой строки текста кнопки буквы
     садились выше середины. */
  line-height: 1;
}

.conn__traffic {
  grid-area: traffic;
  display: flex;
  gap: 16px;
  color: var(--faint);
  font-size: 13px;
}

.conn__message {
  grid-area: msg;
  color: var(--muted);
  font-size: 13px;
}

.conn__message.error {
  color: var(--error);
}

/* Телефон: то же столбиком, кнопка во всю ширину и последней — под
   большим пальцем. Пустые строки сетки не дают лишних отступов: их нет. */
@media (max-width: 640px) {
  .conn {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 8px;
  }

  .conn__toggle {
    order: 1;
    margin-top: 12px;
  }

  .conn__state {
    font-size: 20px;
  }

  .conn__toggle {
    width: 100%;
    height: 44px;
  }
}
</style>
