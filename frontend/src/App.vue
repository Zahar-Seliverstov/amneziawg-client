<template>
  <div class="app">
    <header class="top">
      <span class="top__name">AWG Client</span>
      <span class="faint mono">{{ version }}</span>
      <span class="grow"></span>
      <nav class="tabs" aria-label="Разделы">
        <button class="tab" :class="{ 'tab--on': page === 'routing' }" @click="page = 'routing'">Маршрутизация</button>
        <button class="tab" :class="{ 'tab--on': page === 'settings' }" title="Клавиша ," @click="page = 'settings'">Настройки</button>
      </nav>
    </header>

    <StatusBar
      :status="status"
      :configs-count="configs.length"
      :selected="selected"
      :active-name="activeName"
      :offline="offline"
      @toggle="togglePower"
    />

    <div class="cols">
      <aside class="side">
        <ConfigList :configs="configs" :selected-id="selectedId" :status="status" @select="selectConfig" @changed="load" />
      </aside>

      <main class="main">
        <RoutingPanel v-show="page === 'routing'" />
        <SettingsPanel v-if="page === 'settings'" />
      </main>
    </div>

    <ToastStack />
  </div>
</template>

<script setup lang="ts">
// Главный экран: строка состояния, подключения слева, раздел справа.
//
// Здесь только то, что нужно двум местам сразу: список подключений и
// выбранное из них — и строке состояния, и списку. Остальное живёт в
// хранилищах (stores/) и в самих разделах.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from './api/client'
import StatusBar from './components/StatusBar.vue'
import ToastStack from './components/ToastStack.vue'
import ConfigList from './components/configs/ConfigList.vue'
import RoutingPanel from './components/routing/RoutingPanel.vue'
import SettingsPanel from './components/settings/SettingsPanel.vue'
import { useConfigs } from './composables/useConfigs'
import { useConnectionView } from './composables/useConnectionView'
import { useVpnStatus } from './composables/useVpnStatus'
import { notify, notifyError } from './stores/toasts'

const { status, error: streamError } = useVpnStatus()

// Поток статуса оборван — служба остановлена или перезапускается.
const offline = computed(() => streamError.value !== null)

const { configs, selectedId, selected, activeName, load, select, follow } = useConfigs(() => status.value)
const { isLive } = useConnectionView(() => status.value, () => Boolean(selectedId.value))

const page = ref<'routing' | 'settings'>('routing')
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

// Горячие клавиши вне полей ввода: «,» — настройки, «c» — подключить или
// отключить, Esc — назад к маршрутизации.
function onKey(e: KeyboardEvent) {
  if (e.ctrlKey || e.metaKey || e.altKey) return
  if ((e.target as HTMLElement).closest('input, textarea, [contenteditable]')) return
  if (e.key === ',') page.value = page.value === 'settings' ? 'routing' : 'settings'
  else if (e.key === 'Escape') page.value = 'routing'
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
  display: grid;
  grid-template-rows: auto auto 1fr;
  min-height: 100vh;
  max-width: 1280px;
  margin: 0 auto;
  border-inline: 1px solid var(--line);
  background: var(--panel);
}

.top {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 44px;
  padding: 0 16px;
  border-bottom: 1px solid var(--line);
}

.top__name {
  font-weight: 700;
}

.tabs {
  display: flex;
  align-self: stretch;
}

.tab {
  padding: 0 12px;
  border-bottom: 2px solid transparent;
  color: var(--muted);
}

.tab:hover {
  color: var(--text);
}

.tab--on {
  border-bottom-color: var(--accent);
  color: var(--text);
  font-weight: 600;
}

.cols {
  display: grid;
  grid-template-columns: 320px 1fr;
  min-height: 0;
}

.side {
  border-right: 1px solid var(--line);
  padding: 16px;
  min-width: 0;
}

.main {
  padding: 16px 24px;
  min-width: 0;
}

@media (max-width: 760px) {
  .app {
    border: 0;
  }

  .cols {
    grid-template-columns: 1fr;
  }

  .side {
    border-right: 0;
    border-bottom: 1px solid var(--line);
  }

  .main {
    padding: 14px 16px;
  }
}
</style>
