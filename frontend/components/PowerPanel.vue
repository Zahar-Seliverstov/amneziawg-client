<template>
  <div class="power-area">
    <button
      class="power"
      :class="`power--${powerState}`"
      :disabled="!canToggle"
      @click="$emit('toggle')"
    >
      <span class="power__face">
        <svg class="power__icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <path d="M12 3v9" />
          <path d="M18.4 6.6a9 9 0 1 1-12.8 0" />
        </svg>
        <!-- Подпись меняется вместе с состоянием: подмена текста на месте
             читается как подёргивание, а короткое затухание — как переход. -->
        <Transition name="fade" mode="out-in">
          <span :key="statusText" class="power__label">{{ statusText }}</span>
        </Transition>
        <span v-if="isConnected && status?.connected_at" class="power__time">
          {{ duration }}
        </span>
      </span>
    </button>

    <p class="power__hint">
      <template v-if="!configsCount">Добавьте конфигурацию, чтобы подключиться</template>
      <template v-else-if="isConnected">
        {{ activeName }}
        <button
          type="button"
          class="ping-badge"
          :class="[pingGrade, { 'ping-badge--loading': pingLoading }]"
          :disabled="pingLoading"
          :title="pingTitle"
          @click="refreshPing"
        >
          <!-- Новое число не подменяет прежнее на месте, а сменяет его:
               подмена цифр читается как дрожание, а не как новый замер. -->
          <Transition name="fade" mode="out-in">
            <span :key="pingText">{{ pingText }}</span>
          </Transition>
        </button>
      </template>
      <template v-else-if="selected">{{ selected.name }}</template>
      <template v-else>Выберите конфигурацию</template>
    </p>

    <!-- Причина отказа. Без неё на экране оставалось одно слово «Ошибка»,
         и понять, что случилось, можно было только из системного лога. -->
    <Transition name="fade">
      <p v-if="connectionError" class="power__error">{{ connectionError }}</p>
    </Transition>

    <!-- Блокировка. Показываем именно во время разрыва: только так видно,
         что трафик закрыт, а не утекает открытым, пока идёт восстановление. -->
    <Transition name="fade">
      <p v-if="killSwitchActive" class="power__guard">
        <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
          <rect x="3" y="11" width="18" height="11" rx="2" />
          <path d="M7 11V7a5 5 0 0 1 10 0v4" />
        </svg>
        Трафик мимо туннеля заблокирован
      </p>
    </Transition>
  </div>
</template>

<script setup lang="ts">
import type { AmneziaConfig, ConnectionStatus } from '~/composables/useApi'

// Главная кнопка и всё, что о состоянии соединения говорит рядом с ней:
// время, задержка до сервера, причина отказа, признак блокировки.
//
// Панель получает одно состояние и выводит подписи сама — держать десяток
// готовых строк в свойствах значило бы размазать их вычисление по двум
// файлам. Решение, что делать по нажатию, остаётся за страницей: только она
// знает, есть ли что подключать.
const props = defineProps<{
  status: ConnectionStatus | null
  configsCount: number
  selected: AmneziaConfig | null
  activeName: string
}>()

defineEmits<{ toggle: [] }>()

const {
  isConnected,
  killSwitchActive,
  powerState,
  statusText,
  canToggle,
  connectionError
} = useConnectionView(() => props.status, () => Boolean(props.selected))

const duration = useConnectionDuration(() => props.status?.connected_at)

const {
  text: pingText,
  title: pingTitle,
  grade: pingGrade,
  loading: pingLoading,
  refresh: refreshPing
} = usePing(isConnected)
</script>
