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
      <div class="setting-group">
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
      <div class="setting-group">
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

      <!-- Тема оформления -->
      <div class="setting-group">
        <div class="setting-group__header">
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="5" />
            <path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
          </svg>
          <span>Тема оформления</span>
          <span class="setting-badge">Скоро</span>
        </div>
        <div class="setting-row setting-row--disabled">
          <span class="setting-row__label">Внешний вид</span>
          <SelectMenu
            v-model="themeValue"
            :options="themeOptions"
            class="setting-row__control"
          />
        </div>
      </div>

      <!-- Язык -->
      <div class="setting-group">
        <div class="setting-group__header">
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10" />
            <path d="M2 12h20M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
          </svg>
          <span>Язык</span>
          <span class="setting-badge">Скоро</span>
        </div>
        <div class="setting-row setting-row--disabled">
          <span class="setting-row__label">Язык интерфейса</span>
          <SelectMenu
            v-model="languageValue"
            :options="languageOptions"
            class="setting-row__control"
          />
        </div>
      </div>

      <!-- Информация о приложении -->
      <div class="setting-group">
        <div class="setting-group__header">
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10" />
            <path d="M12 16v-4M12 8h.01" />
          </svg>
          <span>О приложении</span>
        </div>
        <div class="setting-row">
          <span class="setting-row__label">Версия</span>
          <span class="setting-row__value">1.0.0</span>
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
import type { AppSettings, AutostartState } from '~/composables/useApi'

const api = useApi()

const settings = ref<AppSettings>({
  autoconnect: false
})
// null — состояние ещё не удалось получить, '' — конфигурация не выбрана.
const selectedConfigName = ref<string | null>(null)
const autostart = ref<AutostartState>({ enabled: false, available: false })
const saving = ref(false)
const savingAutostart = ref(false)

// Placeholder values for disabled settings
const themeValue = ref('dark')
const languageValue = ref('ru')

const themeOptions = [
  { value: 'dark', label: 'Тёмная' },
  { value: 'light', label: 'Светлая' },
  { value: 'system', label: 'Как в системе' }
]

const languageOptions = [
  { value: 'ru', label: 'Русский' },
  { value: 'en', label: 'English' }
]

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
  const [cfgsRes, settingsRes, selectedRes, autostartRes] = await Promise.allSettled([
    api.getConfigs(),
    api.getSettings(),
    api.getSelectedConfig(),
    api.getAutostart()
  ])

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

  for (const res of [cfgsRes, settingsRes, selectedRes, autostartRes]) {
    if (res.status === 'rejected') console.error('Ошибка загрузки настроек:', res.reason)
  }
})
</script>
