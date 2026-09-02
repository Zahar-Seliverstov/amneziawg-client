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

    <!-- Главная кнопка -->
    <div class="power-area">
      <button
        class="power"
        :class="`power--${powerState}`"
        :disabled="!canToggle"
        @click="togglePower"
      >
        <span class="power__face">
          <svg class="power__icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <path d="M12 3v9" />
            <path d="M18.4 6.6a9 9 0 1 1-12.8 0" />
          </svg>
          <span class="power__label">{{ statusText }}</span>
          <span v-if="isConnected && wsStatus?.connected_at" class="power__time">
            {{ duration }}
          </span>
        </span>
      </button>

      <p class="power__hint">
        <template v-if="!configs.length">Добавьте конфигурацию, чтобы подключиться</template>
        <template v-else-if="isConnected">
          {{ activeConfigName }}
          <button
            type="button"
            class="ping-badge"
            :class="[pingClass, { 'ping-badge--loading': pingLoading }]"
            :disabled="pingLoading"
            :title="pingTitle"
            @click="refreshPing"
          >
            {{ pingText }}
          </button>
        </template>
        <template v-else-if="selectedConfig">{{ selectedConfig.name }}</template>
        <template v-else>Выберите конфигурацию</template>
      </p>

      <!-- Причина отказа. Без неё на экране оставалось одно слово «Ошибка»,
           и понять, что случилось, можно было только из системного лога. -->
      <p v-if="connectionError" class="power__error">{{ connectionError }}</p>

      <!-- Блокировка. Показываем именно во время разрыва: только так видно,
           что трафик закрыт, а не утекает открытым, пока идёт восстановление. -->
      <p v-if="killSwitchActive" class="power__guard">
        <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
          <rect x="3" y="11" width="18" height="11" rx="2" />
          <path d="M7 11V7a5 5 0 0 1 10 0v4" />
        </svg>
        Трафик мимо туннеля заблокирован
      </p>
    </div>

    <!-- Переключение разделов -->
    <nav class="tabs">
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

    <!-- Конфигурации -->
    <section v-if="tab === 'configs'" class="section">
      <div v-if="!showAddConfig" class="section__head">
        <span class="hint">{{ configs.length ? `${configs.length} шт.` : '' }}</span>
        <button class="btn btn--quiet" @click="startAdd">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="3.1" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
          Добавить
        </button>
      </div>

      <ul v-if="configs.length" class="list">
        <li
          v-for="cfg in configs"
          :key="cfg.id"
          class="card"
          :class="{ 'card--open': editingId === cfg.id }"
        >
          <div
            class="row row--tap"
            :class="{ 'row--selected': selectedConfigId === cfg.id }"
            @click="selectConfig(cfg.id)"
          >
            <span class="mark">
              <svg v-if="selectedConfigId === cfg.id" viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="3.3" stroke-linecap="round" stroke-linejoin="round">
                <path d="M20 6 9 17l-5-5" />
              </svg>
            </span>

            <span class="row__name">{{ cfg.name }}</span>

            <span v-if="isConnected && wsStatus?.config_id === cfg.id" class="tag tag--on">
              активна
            </span>

            <button
              class="icon-btn"
              :title="editingId === cfg.id ? 'Свернуть' : 'Изменить'"
              aria-label="Изменить"
              :disabled="wsStatus?.config_id === cfg.id && wsStatus?.state !== 'disconnected'"
              @click.stop="toggleEdit(cfg)"
            >
              <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.7" stroke-linecap="round" stroke-linejoin="round">
                <path d="M12 20h9" />
                <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z" />
              </svg>
            </button>

            <button
              class="icon-btn icon-btn--danger"
              title="Удалить"
              aria-label="Удалить"
              :disabled="wsStatus?.config_id === cfg.id && wsStatus?.state !== 'disconnected'"
              @click.stop="handleDeleteConfig(cfg.id)"
            >
              <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.7" stroke-linecap="round" stroke-linejoin="round">
                <path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6" />
              </svg>
            </button>
          </div>

          <!-- Правка раскрывается прямо в строке: сразу видно, какой конфиг
               меняешь, и незачем искать форму где-то под списком -->
          <div v-if="editingId === cfg.id" class="card__body">
            <div class="field">
              <label>Название</label>
              <input v-model="editName" type="text" class="input" placeholder="Возьмём из адреса сервера" />
            </div>

            <div class="field">
              <label>Содержимое .conf файла</label>
              <textarea v-model="editContent" class="input input--mono"></textarea>
            </div>

            <div class="form__actions">
              <button class="btn" @click="cancelEdit">Отмена</button>
              <button class="btn btn--accent" @click="saveEdit">Сохранить</button>
            </div>
          </div>
        </li>
      </ul>

      <p v-else class="muted">Нет сохранённых конфигураций</p>

      <!-- Добавление прямо на странице, без окна поверх -->
      <div v-if="showAddConfig" class="form">
        <h3 class="form__title">Новая конфигурация</h3>

        <div class="field">
          <label>Название <span class="field__note">необязательно</span></label>
          <input v-model="newConfigName" type="text" class="input" placeholder="Возьмём из адреса сервера" />
        </div>

        <div class="field">
          <label>Содержимое .conf файла</label>
          <textarea
            v-model="newConfigContent"
            class="input input--mono"
            placeholder="[Interface]&#10;PrivateKey = ...&#10;Address = 10.0.0.2/32&#10;&#10;[Peer]&#10;PublicKey = ...&#10;Endpoint = server:51820&#10;AllowedIPs = 0.0.0.0/0"
          ></textarea>
        </div>

        <div class="form__actions">
          <button class="btn" @click="cancelAdd">Отмена</button>
          <button class="btn btn--accent" @click="saveNew">Сохранить</button>
        </div>
      </div>
    </section>

    <!-- Маршрутизация -->
    <section v-else class="section">
      <div class="section__head">
        <span class="hint">Изменения применяются сразу</span>
        <span class="section__tools">
          <button class="btn btn--quiet" title="Сохранить правила в файл" @click="exportRouting">
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="3.1" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 3v12M7 11l5 5 5-5M4 20h16" />
            </svg>
            Выгрузить
          </button>
          <button class="btn btn--quiet" title="Загрузить правила из файла" @click="pickRoutingFile">
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="3.1" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 17V5M7 9l5-5 5 5M4 20h16" />
            </svg>
            Загрузить
          </button>
          <input
            ref="routingFile"
            type="file"
            accept="application/json,.json"
            hidden
            @change="importRouting"
          />
        </span>
      </div>

      <div class="choice">
        <label class="choice__item" :class="{ 'choice__item--on': routingConfig?.mode === 'vpn_list' }">
          <input
            type="radio"
            name="mode"
            value="vpn_list"
            :checked="routingConfig?.mode === 'vpn_list'"
            @change="handleModeChange('vpn_list')"
          />
          <span class="mark mark--radio">
            <svg v-if="routingConfig?.mode === 'vpn_list'" viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="3.3" stroke-linecap="round" stroke-linejoin="round">
              <path d="M20 6 9 17l-5-5" />
            </svg>
          </span>
          <span>
            <span class="choice__title">Только список через VPN</span>
            <span class="choice__note">Остальное идёт напрямую</span>
          </span>
        </label>

        <label class="choice__item" :class="{ 'choice__item--on': routingConfig?.mode === 'direct_list' }">
          <input
            type="radio"
            name="mode"
            value="direct_list"
            :checked="routingConfig?.mode === 'direct_list'"
            @change="handleModeChange('direct_list')"
          />
          <span class="mark mark--radio">
            <svg v-if="routingConfig?.mode === 'direct_list'" viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="3.3" stroke-linecap="round" stroke-linejoin="round">
              <path d="M20 6 9 17l-5-5" />
            </svg>
          </span>
          <span>
            <span class="choice__title">Всё через VPN, кроме списка</span>
            <span class="choice__note">Список идёт напрямую</span>
          </span>
        </label>
      </div>

      <!-- Поиск (показываем только если правил больше 4) -->
      <div v-if="(routingConfig?.rules?.length || 0) > 4" class="search">
        <svg class="search__icon" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.7" stroke-linecap="round">
          <circle cx="10.5" cy="10.5" r="6.5" />
          <path d="M20 20l-4.6-4.6" />
        </svg>

        <input v-model="ruleSearch" type="text" class="search__input" placeholder="Поиск по правилам" />

        <button v-if="ruleSearch" class="icon-btn search__clear" aria-label="Очистить поиск" @click="ruleSearch = ''">
          <svg viewBox="0 0 24 24" width="17" height="17" fill="currentColor">
            <path d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm3.5 12.1-1.4 1.4L12 13.4l-2.1 2.1-1.4-1.4L10.6 12 8.5 9.9l1.4-1.4L12 10.6l2.1-2.1 1.4 1.4L13.4 12z" />
          </svg>
        </button>
      </div>

      <!-- Добавление правила — теперь сверху списка -->
      <div class="add-rule">
        <SelectMenu v-model="newRuleType" :options="ruleTypes" class="add-rule__type" />
        <input
          v-model="newRuleValue"
          type="text"
          class="input"
          :placeholder="rulePlaceholder"
          @keyup.enter="handleAddRule"
        />
        <button class="btn btn--accent" @click="handleAddRule">
          <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.9" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
          Добавить
        </button>
      </div>

      <!-- Список правил -->
      <ul v-if="visibleRules.length" class="list list--tight">
        <li v-for="rule in visibleRules" :key="rule.id" class="row">
          <span class="tag">{{ ruleTypeLabel(rule.type) }}</span>
          <span class="row__value">{{ rule.value }}</span>
          <button class="icon-btn icon-btn--danger" aria-label="Удалить" @click="handleDeleteRule(rule.id)">
            <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.9" stroke-linecap="round">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </li>
      </ul>

      <p v-else class="muted">
        {{ routingConfig?.rules?.length ? 'Ничего не найдено' : 'Правил нет' }}
      </p>
    </section>

    <!-- Уведомления -->
    <TransitionGroup name="toast" tag="div" class="toasts">
      <div v-for="t in toasts" :key="t.id" class="toast" :class="`toast--${t.type}`">
        <span class="toast__text">{{ t.text }}</span>
        <span v-if="t.count > 1" class="toast__count">{{ t.count }}</span>
        <button class="icon-btn toast__close" aria-label="Закрыть" @click="dismiss(t.id)">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="3.1" stroke-linecap="round">
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>

<script setup lang="ts">
import type { AmneziaConfig, RoutingConfig, RoutingMode } from '~/composables/useApi'

const api = useApi()
const { status: wsStatus } = useVpnStatus()

const configs = ref<AmneziaConfig[]>([])
const routingConfig = ref<RoutingConfig | null>(null)

const { toasts, notify, dismiss } = useToasts()

// Ping state
const pingLatency = ref<number | null>(null)
const pingLoading = ref(false)
let pingInterval: ReturnType<typeof setInterval> | null = null

const tab = ref<'configs' | 'routing'>('configs')

// Добавление и правка — две независимые формы: правка живёт внутри строки
// списка, добавление — под ним. Открыта всегда только одна.
const showAddConfig = ref(false)
const newConfigName = ref('')
const newConfigContent = ref('')

const editingId = ref<string | null>(null)
const editName = ref('')
const editContent = ref('')

const ruleSearch = ref('')
const routingFile = ref<HTMLInputElement | null>(null)

const newRuleType = ref<string>('ip')

const ruleTypes = [
  { value: 'ip', label: 'IP' },
  { value: 'cidr', label: 'CIDR' },
  { value: 'domain', label: 'Домен' },
  { value: 'zone', label: 'Зона' }
]
const newRuleValue = ref('')

const selectedConfigId = ref<string | null>(null)
let storedConfigId = ''

const isConnected = computed(() => wsStatus.value?.state === 'connected')
const killSwitchActive = computed(() => Boolean(wsStatus.value?.kill_switch))
const isConnecting = computed(() => wsStatus.value?.state === 'connecting')
const isReconnecting = computed(() => wsStatus.value?.state === 'reconnecting')

// Соединение «живёт»: либо установлено, либо клиент его сейчас поднимает.
// Кнопка в этом состоянии отключает, а выбор другой конфигурации переключает
// туннель, а не начинает подключение с нуля.
const isLive = computed(
  () => isConnected.value || isConnecting.value || isReconnecting.value
)

const selectedConfig = computed(
  () => configs.value.find(c => c.id === selectedConfigId.value) || null
)

const activeConfigName = computed(() => {
  const id = wsStatus.value?.config_id
  return configs.value.find(c => c.id === id)?.name || wsStatus.value?.config_name || ''
})

// Дробная часть нужна только у совсем малых значений: на 42.3ms десятая
// доля ничего не решает, а ширина значка от неё скачет.
const pingText = computed(() => {
  if (pingLatency.value === null) return pingLoading.value ? '…' : '—'
  const ms = pingLatency.value
  return `${ms < 10 ? ms.toFixed(1) : Math.round(ms)}ms`
})

const pingTitle = computed(() => {
  if (pingLatency.value === null) return 'Измерить задержку до сервера'
  return 'Сетевая задержка до сервера, в обход туннеля.\nНажмите, чтобы обновить'
})

const pingClass = computed(() => {
  if (pingLatency.value === null) return ''
  if (pingLatency.value < 100) return 'ping-badge--good'
  if (pingLatency.value < 300) return 'ping-badge--medium'
  return 'ping-badge--bad'
})

const powerState = computed(() => {
  const s = wsStatus.value?.state
  if (s === 'connected') return 'on'
  if (s === 'connecting' || s === 'disconnecting' || s === 'reconnecting') return 'busy'
  if (s === 'error') return 'error'
  return 'off'
})

// Подключение можно прервать: ожидание рукопожатия длится до 45 секунд, и
// всё это время кнопка была заблокирована. Не даём жать только во время
// отключения — там уже идёт разбор соединения, и второй запрос ни к чему.
const canToggle = computed(() => {
  if (wsStatus.value?.state === 'disconnecting') return false
  if (isLive.value) return true
  return Boolean(selectedConfigId.value)
})

// Причину показываем и при отказе, и при потере связи. Второе особенно важно:
// без объяснения «Переподключение» выглядит как зависшая кнопка.
const connectionError = computed(() => {
  const s = wsStatus.value
  if (!s) return null

  if (s.state === 'error') {
    return s.error || 'Не удалось подключиться. Подробности — в системном журнале'
  }

  if (s.state === 'reconnecting') {
    const reason = s.error || 'сервер не отвечает'
    const attempt = s.attempt ? `, попытка ${s.attempt}` : ''
    return `Связь потеряна: ${reason}${attempt}`
  }

  return null
})

const statusText = computed(() => {
  switch (wsStatus.value?.state) {
    case 'connected': return 'Подключено'
    case 'connecting': return 'Подключение'
    case 'reconnecting': return 'Переподключение'
    case 'disconnecting': return 'Отключение'
    case 'error': return 'Ошибка'
    default: return 'Отключено'
  }
})

const now = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | null = null

const duration = computed(() => {
  const at = wsStatus.value?.connected_at
  if (!at) return ''

  const diff = Math.max(0, Math.floor((now.value - new Date(at).getTime()) / 1000))
  const h = Math.floor(diff / 3600)
  const m = Math.floor((diff % 3600) / 60)
  const sec = diff % 60
  const pad = (n: number) => String(n).padStart(2, '0')

  return h > 0 ? `${h}:${pad(m)}:${pad(sec)}` : `${m}:${pad(sec)}`
})

const rulePlaceholder = computed(() => {
  switch (newRuleType.value) {
    case 'ip': return '1.1.1.1'
    case 'cidr': return '10.0.0.0/8'
    case 'domain': return 'google.com'
    case 'zone': return '.ru'
    default: return ''
  }
})

const visibleRules = computed(() => {
  const rules = routingConfig.value?.rules || []
  const q = ruleSearch.value.trim().toLowerCase()
  if (!q) return rules

  return rules.filter(
    r => r.value.toLowerCase().includes(q) || ruleTypeLabel(r.type).toLowerCase().includes(q)
  )
})

// Замер задержки. Цель выбирает бэкенд: он знает и активный конфиг, и
// состояние подключения, и какие маршруты поставил, — на фронте это
// пришлось бы угадывать разбором конфига.
async function measurePing() {
  if (!isConnected.value) {
    pingLatency.value = null
    return
  }

  pingLoading.value = true

  try {
    const res = await api.ping()

    if (res.success && typeof res.latency === 'number') {
      pingLatency.value = res.latency
    } else {
      pingLatency.value = null
    }
  } catch (e) {
    console.error('Ping error:', e)
    pingLatency.value = null
  } finally {
    pingLoading.value = false
  }
}

function startPingInterval() {
  stopPingInterval()
  measurePing()
  pingInterval = setInterval(measurePing, 10000) // Every 10 seconds
}

// Клик по значку меряет пинг сразу. Отсчёт при этом начинается заново,
// иначе следующий автоматический замер мог прийти через долю секунды
// после ручного и сбить только что показанное число.
function refreshPing() {
  if (pingLoading.value) return
  startPingInterval()
}

function stopPingInterval() {
  if (pingInterval) {
    clearInterval(pingInterval)
    pingInterval = null
  }
  pingLatency.value = null
  pingLoading.value = false
}

// Watch connection state for ping
watch(isConnected, (connected) => {
  if (connected) {
    startPingInterval()
  } else {
    stopPingInterval()
  }
})

// Also watch configs - ping might need them to get endpoint host
watch(configs, () => {
  if (isConnected.value && !pingInterval) {
    startPingInterval()
  }
})

function exportRouting() {
  if (!routingConfig.value) return

  const rulesCount = routingConfig.value.rules?.length || 0
  const body = JSON.stringify(
    { mode: routingConfig.value.mode, rules: routingConfig.value.rules },
    null,
    2
  )

  const url = URL.createObjectURL(new Blob([body], { type: 'application/json' }))
  const a = document.createElement('a')
  a.href = url
  a.download = `awg-routing-${new Date().toISOString().slice(0, 10)}.json`
  a.click()
  URL.revokeObjectURL(url)
  
  notify(`Выгружено правил: ${rulesCount}`, 'ok')
}

function pickRoutingFile() {
  routingFile.value?.click()
}

async function importRouting(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return

  try {
    const data = JSON.parse(await file.text())

    if (!data || typeof data !== 'object' || !Array.isArray(data.rules)) {
      throw new Error('В файле нет списка правил')
    }

    await api.setRouting({ mode: data.mode, rules: data.rules })
    ruleSearch.value = ''
    await loadData()
    notify(`Загружено правил: ${data.rules.length}`, 'ok')
  } catch (err: any) {
    notify(err instanceof SyntaxError ? 'Файл не является корректным JSON' : err.message)
  } finally {
    input.value = ''
  }
}

function ruleTypeLabel(type: string) {
  switch (type) {
    case 'ip': return 'IP'
    case 'cidr': return 'CIDR'
    case 'domain': return 'Домен'
    case 'zone': return 'Зона'
    default: return type
  }
}

async function selectConfig(id: string) {
  // Во время разбора соединения ждём: там уже снимаются маршруты.
  if (wsStatus.value?.state === 'disconnecting') return
  if (id === selectedConfigId.value) return

  selectedConfigId.value = id
  await persistSelectedConfig(id)

  // На живом соединении выбор другого конфига переключает туннель сразу.
  // Бэкенд сам разбирает текущий и поднимает новый, поэтому отдельного
  // «отключить» здесь нет — иначе между двумя запросами возникала бы щель,
  // в которой маршруты успевали слететь.
  if (isLive.value) {
    try {
      await api.connect(id)
    } catch (e: any) {
      notify(e.message)
    }
  }
}

// Выбор конфига живёт на backend: к нему подключается автоподключение.
// silent — для выбора по умолчанию при загрузке: он делается без ведома
// пользователя, и ругаться на него всплывающим сообщением незачем.
async function persistSelectedConfig(id: string | null, silent = false) {
  const next = id ?? ''
  if (next === storedConfigId) return
  try {
    await api.setSelectedConfig(next)
    storedConfigId = next
  } catch (e: any) {
    if (!silent) notify(e.message)
  }
}

async function togglePower() {
  if (!canToggle.value) return

  try {
    if (isLive.value) {
      await api.disconnect()
    } else if (selectedConfigId.value) {
      await api.connect(selectedConfigId.value)
    }
  } catch (e: any) {
    notify(e.message)
  }
}

async function loadData() {
  try {
    // Выбранный конфиг — не критичная часть загрузки: со старым backend'ом
    // эндпоинта может не быть, но список конфигураций всё равно должен
    // отрисоваться.
    const [cfgs, routing] = await Promise.all([api.getConfigs(), api.getRouting()])
    const storedId = await api.getSelectedConfig().catch(() => '')
    configs.value = cfgs || []
    routingConfig.value = routing
    storedConfigId = storedId

    const active = wsStatus.value?.config_id
    const exists = (id?: string | null) => Boolean(id) && configs.value.some(c => c.id === id)

    if (exists(active)) {
      selectedConfigId.value = active!
    } else if (exists(storedId)) {
      selectedConfigId.value = storedId
    } else if (!exists(selectedConfigId.value)) {
      selectedConfigId.value = configs.value[0]?.id ?? null
    }

    // Подхваченный по умолчанию конфиг тоже запоминаем — иначе
    // автоподключение при следующем запуске не будет знать, к чему цепляться.
    await persistSelectedConfig(selectedConfigId.value, true)
  } catch (e: any) {
    notify(e.message)
  }
}

// Повторное нажатие на карандаш сворачивает форму — открывать её нечем,
// кроме той же кнопки, поэтому она и закрывает.
function toggleEdit(cfg: AmneziaConfig) {
  if (editingId.value === cfg.id) {
    cancelEdit()
    return
  }

  cancelAdd()
  editingId.value = cfg.id
  editName.value = cfg.name
  editContent.value = cfg.raw_config
}

function cancelEdit() {
  editingId.value = null
  editName.value = ''
  editContent.value = ''
}

async function saveEdit() {
  if (!editingId.value) return

  if (!editContent.value) {
    notify('Содержимое .conf файла не может быть пустым')
    return
  }

  try {
    // Пустое название backend заполнит сам — из адреса сервера.
    await api.updateConfig(editingId.value, editName.value.trim(), editContent.value)
    cancelEdit()
    await loadData()
  } catch (e: any) {
    notify(e.message)
  }
}

function startAdd() {
  cancelEdit()
  showAddConfig.value = true
}

function cancelAdd() {
  showAddConfig.value = false
  newConfigName.value = ''
  newConfigContent.value = ''
}

async function saveNew() {
  if (!newConfigContent.value) {
    notify('Вставьте содержимое .conf файла')
    return
  }

  try {
    await api.addConfig(newConfigName.value.trim(), newConfigContent.value)
    cancelAdd()
    await loadData()
  } catch (e: any) {
    notify(e.message)
  }
}

async function handleDeleteConfig(id: string) {
  try {
    await api.deleteConfig(id)
    if (editingId.value === id) cancelEdit()
    await loadData()
  } catch (e: any) {
    notify(e.message)
  }
}

async function handleModeChange(mode: RoutingMode) {
  try {
    await api.setRoutingMode(mode)
    await loadData()
  } catch (e: any) {
    notify(e.message)
  }
}

async function handleAddRule() {
  if (!newRuleValue.value) return

  try {
    await api.addRoutingRule(newRuleType.value, newRuleValue.value)
    const addedValue = newRuleValue.value
    newRuleValue.value = ''
    await loadData()
    notify(`Добавлено: ${addedValue}`, 'ok')
  } catch (e: any) {
    notify(e.message)
  }
}

async function handleDeleteRule(id: string) {
  // Find rule to show its value in notification
  const rule = routingConfig.value?.rules?.find(r => r.id === id)
  const ruleValue = rule?.value || ''
  
  try {
    await api.deleteRoutingRule(id)
    await loadData()
    notify(`Удалено: ${ruleValue}`, 'ok')
  } catch (e: any) {
    notify(e.message)
  }
}

watch(() => wsStatus.value?.config_id, id => {
  if (id && configs.value.some(c => c.id === id)) {
    selectedConfigId.value = id
    storedConfigId = id
  }
})

onMounted(async () => {
  await loadData()
  ticker = setInterval(() => { now.value = Date.now() }, 1000)
  
  // Start ping if already connected
  if (isConnected.value) {
    startPingInterval()
  }
})

onUnmounted(() => {
  if (ticker) clearInterval(ticker)
  stopPingInterval()
})
</script>
