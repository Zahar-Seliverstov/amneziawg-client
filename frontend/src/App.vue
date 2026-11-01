<template>
  <div class="app">
    <header class="top">
      <span class="top__name">AWG Client</span>
      <span class="grow"></span>
      <span v-if="pending" class="top__pending" role="status">
        <LoaderCircle class="spin" aria-hidden="true" />Применяется…
      </span>
      <button
        class="btn btn--ghost btn--icon"
        :class="{ 'btn--on': page === 'settings' }"
        :aria-pressed="page === 'settings'"
        aria-label="Настройки"
        title="Настройки · клавиша ,"
        @click="toggle()"
      >
        <Settings aria-hidden="true" />
      </button>
    </header>

    <main class="page">
      <ConnectionCard
        v-if="page === 'main'"
        :status="status"
        :offline="offline"
        :configs="configs"
        :protocols="protocols"
        :selected-id="selectedId"
        :active-name="activeName"
        @toggle="togglePower"
        @select="selectConfig"
        @changed="load"
      />
      <!-- Маршрутизация не размонтируется при уходе в настройки: набранное
           в поле правил и свёрнутые группы должны дождаться возвращения. -->
      <RoutingPanel v-show="page === 'main'" />
      <SettingsPanel v-if="page === 'settings'" :version="version" @back="open('main')" />
    </main>

    <ToastStack />
  </div>
</template>

<script setup lang="ts">
// Главный экран: соединение сверху, правила под ним; настройки —
// отдельным экраном.
//
// Здесь только то, что нужно нескольким местам сразу: список подключений и
// выбранное из них. Остальное живёт в хранилищах (stores/) и в разделах.
import { LoaderCircle, Settings } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from './api/client'
import ToastStack from './components/ToastStack.vue'
import ConnectionCard from './components/connection/ConnectionCard.vue'
import RoutingPanel from './components/routing/RoutingPanel.vue'
import SettingsPanel from './components/settings/SettingsPanel.vue'
import { useConfigs } from './composables/useConfigs'
import { useConnectionView } from './composables/useConnectionView'
import { usePage } from './composables/usePage'
import { useVpnStatus } from './composables/useVpnStatus'
import { useRouting } from './stores/routing'
import { notify, notifyError } from './stores/toasts'

const { status, error: streamError } = useVpnStatus()

// Поток статуса оборван — служба остановлена или перезапускается.
const offline = computed(() => streamError.value !== null)

const { configs, protocols, selectedId, activeName, load, select, follow } = useConfigs(() => status.value)
const { isLive } = useConnectionView(() => status.value, () => Boolean(selectedId.value))
const { page, open, toggle } = usePage()
const { pending } = useRouting()

const version = ref('')

async function togglePower() {
  try {
    if (isLive.value) await api.disconnect()
    else if (selectedId.value) await api.connect(selectedId.value)
  } catch (e) {
    notifyError(e)
  }
}

function selectConfig(id: string) {
  // Во время разбора соединения ждём: там уже снимаются маршруты.
  if (status.value?.state === 'disconnecting') return
  select(id, isLive.value)
}

// Подключиться могли и мимо этого экрана — автоподключением при запуске.
// Выбранным тогда становится то подключение, на котором держится туннель.
watch(() => status.value?.config_id, id => follow(id))

// Правила применяются с задержкой, и отказ приходит потоком состояния.
// Говорим только о новом отказе, иначе каждое обновление счётчиков
// повторяло бы то же сообщение.
watch(() => status.value?.routing_error, (reason, was) => {
  if (reason && reason !== was) notify(`Правила не применились: ${reason}`)
})

// Горячие клавиши вне полей ввода и окон: «,» — настройки, «c» — подключить
// или отключить, Esc — назад к главному экрану. В открытом окне (форма
// подключения, выбор программы) буква «c» — не команда.
function onKey(e: KeyboardEvent) {
  if (e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return
  if ((e.target as HTMLElement).closest('input, textarea, [contenteditable], dialog, [role="dialog"]')) return
  if (e.key === ',') toggle()
  else if (e.key === 'Escape') open('main')
  else if ((e.key === 'c' || e.key === 'с') && !offline.value) togglePower()
}

onMounted(async () => {
  window.addEventListener('keydown', onKey)
  load()
  try {
    version.value = await api.getVersion()
  } catch {}
})

onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<style scoped>
.app {
  min-height: 100vh;
}

.top {
  position: sticky;
  top: 0;
  z-index: 30;
  display: flex;
  align-items: center;
  gap: 12px;
  max-width: 720px;
  height: 56px;
  margin: 0 auto;
  padding: 0 24px;
  background: var(--bg);
}

.top__name {
  font-weight: 600;
}

.top__pending {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--faint);
  font-size: 13px;
}

.top__pending svg {
  width: 14px;
  height: 14px;
}

.top .btn--icon {
  margin-right: -6px;
}

.page {
  display: grid;
  gap: 40px;
  max-width: 720px;
  margin: 0 auto;
  padding: 16px 24px 64px;
}

@media (max-width: 640px) {
  .top {
    padding: 0 16px;
  }

  .page {
    gap: 32px;
    padding: 8px 16px 48px;
  }
}
</style>
