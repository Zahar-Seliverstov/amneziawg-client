<template>
  <div class="app">
    <header class="topbar">
      <span class="topbar__brand">
        <span class="topbar__logo" aria-hidden="true"></span>
        <h1>AWG Client</h1>
      </span>
      <button class="icon-btn topbar__settings" title="Настройки" @click="$router.push('/settings')">
        <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="3" />
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" />
        </svg>
      </button>
    </header>

    <PowerPanel
      :status="status"
      :configs-count="configs.length"
      :selected="selected"
      :active-name="activeName"
      @toggle="togglePower"
    />

    <nav class="tabs" :class="`tabs--${tab}`">
      <!-- Подложка выбранной вкладки переезжает, а не зажигается на новом
           месте: так переключение читается как движение, а не как мигание. -->
      <span class="tabs__slider" aria-hidden="true"></span>

      <button
        class="tabs__btn"
        :class="{ 'tabs__btn--on': tab === 'configs' }"
        @click="tab = 'configs'"
      >
        Конфигурации
      </button>
      <button
        class="tabs__btn"
        :class="{ 'tabs__btn--on': tab === 'routing' }"
        @click="tab = 'routing'"
      >
        Маршрутизация
      </button>
    </nav>

    <!-- out-in: разделы разной высоты, и показывать их одновременно значит
         дёргать страницу. -->
    <Transition name="tab" mode="out-in">
      <ConfigList
        v-if="tab === 'configs'"
        :configs="configs"
        :selected-id="selectedId"
        :status="status"
        @select="selectConfig"
        @changed="load"
        @notify="notify"
      />

      <RoutingPanel v-else @notify="notify" />
    </Transition>

    <ToastStack :toasts="toasts" @dismiss="dismiss" />
  </div>
</template>

<script setup lang="ts">
// Главный экран: кнопка подключения, список конфигураций и маршрутизация.
//
// Страница только связывает части между собой. Своё состояние она держит
// ровно там, где оно нужно двум разным местам сразу: список конфигураций и
// выбранная из них нужны и кнопке, и списку, поэтому живут здесь. Правила
// маршрутизации нужны одной панели — там они и живут.
const { status } = useVpnStatus()
const { toasts, notify, dismiss } = useToasts()

const { configs, selectedId, selected, activeName, load, select, follow } = useConfigs(
  () => status.value,
  notify
)

const { isLive } = useConnectionView(() => status.value, () => Boolean(selectedId.value))

const tab = ref<'configs' | 'routing'>('configs')

const api = useApi()

async function togglePower() {
  try {
    if (isLive.value) {
      await api.disconnect()
    } else if (selectedId.value) {
      await api.connect(selectedId.value)
    }
  } catch (e: any) {
    notify(e.message)
  }
}

function selectConfig(id: string) {
  // Во время разбора соединения ждём: там уже снимаются маршруты.
  if (status.value?.state === 'disconnecting') return
  select(id, isLive.value)
}

// Подключиться могли и мимо этого экрана — из трея или автоподключением при
// запуске. Выбранной тогда становится та конфигурация, на которой держится
// соединение.
watch(() => status.value?.config_id, id => follow(id))

onMounted(load)
</script>
