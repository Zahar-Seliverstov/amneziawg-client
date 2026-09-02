<template>
  <div class="app">
    <header class="topbar">
      <button class="icon-btn topbar__back" title="Назад" @click="$router.push('/')">
        <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M19 12H5M12 19l-7-7 7-7" />
        </svg>
      </button>
      <h1>Настройки</h1>
    </header>

    <section class="section">
      <!-- Автоподключение -->
      <div class="setting-group rise">
        <div class="setting-group__header">
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M5 12.55a11 11 0 0 1 14 0M8.53 16.11a6 6 0 0 1 6.95 0M12 20h.01" />
          </svg>
          <span>Автоподключение</span>
        </div>
        <label class="setting-row">
          <span class="setting-row__label">
            <span>Автоматическое подключение</span>
            <span class="setting-row__note">При запуске приложения подключаться к конфигурации, выбранной на главном экране</span>
          </span>
          <span class="toggle" :class="{ 'toggle--disabled': saving }">
            <input 
              type="checkbox" 
              v-model="settings.autoconnect" 
              :disabled="saving"
              @change="saveSettings"
            />
            <span class="toggle__track">
              <span class="toggle__thumb"></span>
            </span>
          </span>
        </label>
        <div v-if="settings.autoconnect" class="setting-row setting-row--sub">
          <span class="setting-row__label">Конфигурация</span>
          <span class="setting-row__value">{{ selectedConfigLabel }}</span>
        </div>
        <div v-if="settings.autoconnect && selectedConfigName === ''" class="setting-row setting-row--sub">
          <span class="setting-row__note">
            Пока конфигурация не выбрана на главном экране, автоподключение не сработает.
          </span>
        </div>
      </div>

      <!-- Автозапуск -->
      <div class="setting-group rise">
        <div class="setting-group__header">
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 2v10M18.36 6.64a9 9 0 1 1-12.73 0" />
          </svg>
          <span>Автозапуск</span>
        </div>
        <label class="setting-row" :class="{ 'setting-row--disabled': !autostart.available }">
          <span class="setting-row__label">
            <span>Запускать при входе в систему</span>
            <span class="setting-row__note">
              {{ autostart.available
                ? 'Приложение стартует свёрнутым в трей — окно откроется по клику на значке'
                : autostart.reason || 'Автозапуск недоступен на этой машине' }}
            </span>
          </span>
          <span class="toggle" :class="{ 'toggle--disabled': savingAutostart || !autostart.available }">
            <input 
              type="checkbox" 
              v-model="autostart.enabled" 
              :disabled="savingAutostart || !autostart.available"
              @change="saveAutostart"
            />
            <span class="toggle__track">
              <span class="toggle__thumb"></span>
            </span>
          </span>
        </label>
      </div>

      <!-- Блокировка трафика мимо туннеля -->
      <div class="setting-group rise">
        <div class="setting-group__header">
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="11" width="18" height="11" rx="2" />
            <path d="M7 11V7a5 5 0 0 1 10 0v4" />
          </svg>
          <span>Блокировка трафика</span>
          <span class="setting-badge setting-badge--beta">Эксперимент</span>
        </div>
        <label class="setting-row" :class="{ 'setting-row--disabled': !killSwitch.available }">
          <span class="setting-row__label">
            <span>Не выпускать трафик мимо туннеля</span>
            <span class="setting-row__note">
              {{ killSwitchNote }}
            </span>
          </span>
          <span class="toggle" :class="{ 'toggle--disabled': savingKillSwitch || !killSwitch.available }">
            <input
              type="checkbox"
              v-model="killSwitch.enabled"
              :disabled="savingKillSwitch || !killSwitch.available"
              @change="saveKillSwitch"
            />
            <span class="toggle__track">
              <span class="toggle__thumb"></span>
            </span>
          </span>
        </label>
      </div>

      <!-- Оформление и язык: сделаны, но ещё не работают. Показываем
           выключенными, а не прячем — так видно, что они запланированы, и
           их не приходится искать в следующей версии заново. -->
      <div class="setting-group setting-group--soon rise">
        <div class="setting-group__header">
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="9" />
            <path d="M12 3a9 9 0 0 0 0 18 4.5 4.5 0 0 0 0-9 4.5 4.5 0 0 1 0-9z" />
          </svg>
          <span>Тема</span>
          <span class="setting-badge">Скоро</span>
        </div>
        <div class="setting-row setting-row--disabled">
          <span class="setting-row__label">
            <span>Оформление</span>
            <span class="setting-row__note">Пока только тёмное — светлое появится в следующих версиях</span>
          </span>
          <span class="setting-row__value">Тёмное</span>
        </div>
      </div>

      <div class="setting-group setting-group--soon rise">
        <div class="setting-group__header">
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="9" />
            <path d="M3 12h18M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18z" />
          </svg>
          <span>Язык</span>
          <span class="setting-badge">Скоро</span>
        </div>
        <div class="setting-row setting-row--disabled">
          <span class="setting-row__label">
            <span>Язык интерфейса</span>
            <span class="setting-row__note">Пока только русский — перевод появится в следующих версиях</span>
          </span>
          <span class="setting-row__value">Русский</span>
        </div>
      </div>

      <!-- Информация о приложении -->
      <div class="setting-group rise">
        <div class="setting-group__header">
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10" />
            <path d="M12 16v-4M12 8h.01" />
          </svg>
          <span>О приложении</span>
        </div>
        <div class="setting-row">
          <span class="setting-row__label">Версия</span>
          <span class="setting-row__value">{{ version || '—' }}</span>
        </div>
      </div>
    </section>

    <!-- Уведомления -->
    <TransitionGroup name="toast" tag="div" class="toasts">
      <div v-for="t in toasts" :key="t.id" class="toast" :class="`toast--${t.type}`">
        <span class="toast__text">{{ t.text }}</span>
        <span v-if="t.count > 1" class="toast__count">{{ t.count }}</span>
        <button class="icon-btn toast__close" aria-label="Закрыть" @click="dismissToast(t.id)">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="3.1" stroke-linecap="round">
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>

<script setup lang="ts">
import type { AppSettings, AutostartState, KillSwitchState } from '~/composables/useApi'

const api = useApi()

const settings = ref<AppSettings>({
  autoconnect: false
})
// null — состояние ещё не удалось получить, '' — конфигурация не выбрана.
const selectedConfigName = ref<string | null>(null)
const autostart = ref<AutostartState>({ enabled: false, available: false })
const killSwitch = ref<KillSwitchState>({ enabled: false, available: false, active: false })
const saving = ref(false)
const savingAutostart = ref(false)
const savingKillSwitch = ref(false)

// Объяснение важнее переключателя. Блокировка режет ЛВС и работает не при
// любой маршрутизации, и узнать об этом постфактум — худший вариант.
const killSwitchNote = computed(() => {
  if (!killSwitch.value.available) {
    return killSwitch.value.reason || 'Блокировка недоступна на этой машине'
  }
  if (killSwitch.value.enabled && killSwitch.value.reason) {
    return killSwitch.value.reason
  }
  return 'Возможность экспериментальная: проверена не на всех системах, поэтому включайте её, '
    + 'если готовы к тому, что связь придётся чинить руками. '
    + 'При обрыве туннеля трафик не уходит в обход, а блокируется до восстановления связи. '
    + 'Пока блокировка действует, локальная сеть тоже недоступна. '
    + 'Работает только когда весь трафик идёт через VPN, без исключений в маршрутизации'
})

// Версия приходит от backend'а: единственный источник — манифест Tauri,
// откуда её подставляют на сборке.
const version = ref('')

const selectedConfigLabel = computed(() => {
  if (selectedConfigName.value === null) return '—'
  return selectedConfigName.value || 'Не выбрана'
})

// Всплывающие сообщения — общие с главным экраном: одинаковые слипаются
// в одну карточку со счётчиком.
const { toasts, notify, dismiss: dismissToast } = useToasts('ok')

async function saveSettings() {
  saving.value = true
  try {
    await api.setSettings(settings.value)
    notify('Настройки сохранены')
  } catch (e: any) {
    notify(e.message || 'Ошибка сохранения', 'error')
  } finally {
    saving.value = false
  }
}

// Блокировка живёт своим эндпоинтом: у неё есть доступность, и включение
// применяется к живому туннелю сразу.
async function saveKillSwitch() {
  savingKillSwitch.value = true
  const desired = killSwitch.value.enabled
  try {
    killSwitch.value = await api.setKillSwitch(desired)
    notify(desired ? 'Блокировка включена' : 'Блокировка выключена')
  } catch (e: any) {
    killSwitch.value.enabled = !desired
    notify(e.message || 'Не удалось изменить блокировку', 'error')
  } finally {
    savingKillSwitch.value = false
  }
}

// Автозапуск живёт не в настройках приложения, а ярлыком в системе, поэтому
// сохраняется отдельным запросом и состояние берём из ответа backend'а.
async function saveAutostart() {
  savingAutostart.value = true
  const desired = autostart.value.enabled
  try {
    autostart.value = await api.setAutostart(desired)
    notify(desired ? 'Автозапуск включён' : 'Автозапуск выключен')
  } catch (e: any) {
    autostart.value.enabled = !desired
    notify(e.message || 'Не удалось изменить автозапуск', 'error')
  } finally {
    savingAutostart.value = false
  }
}

// Каждый блок настроек грузим независимо: недоступный эндпоинт (например,
// backend старее интерфейса) не должен обнулять остальные — иначе страница
// врёт, что автоподключение выключено, а конфигурация не выбрана.
onMounted(async () => {
  const [cfgsRes, settingsRes, selectedRes, autostartRes, versionRes, killSwitchRes] = await Promise.allSettled([
    api.getConfigs(),
    api.getSettings(),
    api.getSelectedConfig(),
    api.getAutostart(),
    api.getVersion(),
    api.getKillSwitch()
  ])

  if (versionRes.status === 'fulfilled') {
    version.value = versionRes.value
  }

  if (killSwitchRes.status === 'fulfilled') {
    killSwitch.value = killSwitchRes.value
  } else {
    killSwitch.value = {
      enabled: false,
      available: false,
      active: false,
      reason: 'Backend не ответил на запрос о блокировке — обнови и перезапусти его'
    }
  }

  if (settingsRes.status === 'fulfilled') {
    settings.value = settingsRes.value
  }

  if (cfgsRes.status === 'fulfilled' && selectedRes.status === 'fulfilled') {
    selectedConfigName.value = (cfgsRes.value || []).find(c => c.id === selectedRes.value)?.name || ''
  }

  if (autostartRes.status === 'fulfilled') {
    autostart.value = autostartRes.value
  } else {
    autostart.value = {
      enabled: false,
      available: false,
      reason: 'Backend не ответил на запрос об автозапуске — обнови и перезапусти его'
    }
  }

  for (const res of [cfgsRes, settingsRes, selectedRes, autostartRes, versionRes, killSwitchRes]) {
    if (res.status === 'rejected') console.error('Ошибка загрузки настроек:', res.reason)
  }
})
</script>
