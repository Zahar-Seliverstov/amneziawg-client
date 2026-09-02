<template>
  <section class="section">
    <div class="section__head">
      <span class="hint">Изменения применяются сразу</span>
      <span class="section__tools">
        <button class="btn btn--quiet" title="Сохранить правила в файл" @click="exportRules">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="3.1" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 3v12M7 11l5 5 5-5M4 20h16" />
          </svg>
          Выгрузить
        </button>
        <button class="btn btn--quiet" title="Загрузить правила из файла" @click="pickFile">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="3.1" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 17V5M7 9l5-5 5 5M4 20h16" />
          </svg>
          Загрузить
        </button>
        <input
          ref="fileInput"
          type="file"
          accept="application/json,.json"
          hidden
          @change="onFile"
        />
      </span>
    </div>

    <div class="choice">
      <label
        v-for="mode in modes"
        :key="mode.value"
        class="choice__item"
        :class="{ 'choice__item--on': routing?.mode === mode.value }"
      >
        <input
          type="radio"
          name="mode"
          :value="mode.value"
          :checked="routing?.mode === mode.value"
          @change="setMode(mode.value)"
        />
        <span class="mark mark--radio">
          <svg v-if="routing?.mode === mode.value" viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="3.3" stroke-linecap="round" stroke-linejoin="round">
            <path d="M20 6 9 17l-5-5" />
          </svg>
        </span>
        <span>
          <span class="choice__title">{{ mode.title }}</span>
          <span class="choice__note">{{ mode.note }}</span>
        </span>
      </label>
    </div>

    <!-- Поиск нужен, только когда правил столько, что глазами уже не найти -->
    <div v-if="(routing?.rules?.length || 0) > 4" class="search">
      <svg class="search__icon" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.7" stroke-linecap="round">
        <circle cx="10.5" cy="10.5" r="6.5" />
        <path d="M20 20l-4.6-4.6" />
      </svg>

      <input v-model="search" type="text" class="search__input" placeholder="Поиск по правилам" />

      <button v-if="search" class="icon-btn search__clear" aria-label="Очистить поиск" @click="search = ''">
        <svg viewBox="0 0 24 24" width="17" height="17" fill="currentColor">
          <path d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm3.5 12.1-1.4 1.4L12 13.4l-2.1 2.1-1.4-1.4L10.6 12 8.5 9.9l1.4-1.4L12 10.6l2.1-2.1 1.4 1.4L13.4 12z" />
        </svg>
      </button>
    </div>

    <!-- Добавление правила — над списком: список бывает длинным -->
    <div class="add-rule">
      <SelectMenu v-model="newType" :options="ruleTypes" class="add-rule__type" />
      <input
        v-model="newValue"
        type="text"
        class="input"
        :placeholder="placeholder"
        @keyup.enter="addRule"
      />
      <button class="btn btn--accent" @click="addRule">
        <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.9" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
        Добавить
      </button>
    </div>

    <ul v-if="visibleRules.length" class="list list--tight">
      <li v-for="rule in visibleRules" :key="rule.id" class="row">
        <span class="tag">{{ typeLabel(rule.type) }}</span>
        <span class="row__value">{{ rule.value }}</span>
        <button class="icon-btn icon-btn--danger" aria-label="Удалить" @click="deleteRule(rule.id)">
          <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.9" stroke-linecap="round">
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </button>
      </li>
    </ul>

    <p v-else class="muted">
      {{ routing?.rules?.length ? 'Ничего не найдено' : 'Правил нет' }}
    </p>
  </section>
</template>

<script setup lang="ts">
import type { RoutingConfig, RoutingMode } from '~/composables/useApi'

// Правила маршрутизации: режим, поиск, добавление и список.
//
// Панель самодостаточна — правила больше нигде на странице не нужны, поэтому
// и читает, и меняет их она сама. Наверх уходят только сообщения
// пользователю: очередь уведомлений принадлежит странице.
//
// Изменения применяются сразу, на живом туннеле, поэтому после каждой
// операции список перечитывается: правило могло лечь не целиком — например,
// имя не разрешилось, — и показывать надо то, что действительно записано.
const emit = defineEmits<{ notify: [text: string, type?: 'error' | 'ok'] }>()

const api = useApi()

const routing = ref<RoutingConfig | null>(null)

const modes = [
  { value: 'vpn_list' as RoutingMode, title: 'Только список через VPN', note: 'Остальное идёт напрямую' },
  { value: 'direct_list' as RoutingMode, title: 'Всё через VPN, кроме списка', note: 'Список идёт напрямую' }
]

const ruleTypes = [
  { value: 'ip', label: 'IP' },
  { value: 'cidr', label: 'CIDR' },
  { value: 'domain', label: 'Домен' },
  { value: 'zone', label: 'Зона' }
]

const examples: Record<string, string> = {
  ip: '1.1.1.1',
  cidr: '10.0.0.0/8',
  domain: 'google.com',
  zone: '.ru'
}

const search = ref('')
const newType = ref('ip')
const newValue = ref('')
const fileInput = ref<HTMLInputElement | null>(null)

const placeholder = computed(() => examples[newType.value] || '')

function typeLabel(type: string) {
  return ruleTypes.find(t => t.value === type)?.label || type
}

const visibleRules = computed(() => {
  const rules = routing.value?.rules || []
  const q = search.value.trim().toLowerCase()
  if (!q) return rules

  return rules.filter(
    r => r.value.toLowerCase().includes(q) || typeLabel(r.type).toLowerCase().includes(q)
  )
})

async function load() {
  try {
    routing.value = await api.getRouting()
  } catch (e: any) {
    emit('notify', e.message)
  }
}

async function setMode(mode: RoutingMode) {
  try {
    await api.setRoutingMode(mode)
  } catch (e: any) {
    emit('notify', e.message)
  }
  await load()
}

async function addRule() {
  if (!newValue.value) return

  const value = newValue.value

  try {
    await api.addRoutingRule(newType.value, value)
    // Поле очищаем только после согласия службы: отвергнутое значение чаще
    // всего надо поправить, а не набирать заново.
    newValue.value = ''
    await load()
    emit('notify', `Добавлено: ${value}`, 'ok')
  } catch (e: any) {
    emit('notify', e.message)
  }
}

async function deleteRule(id: string) {
  const value = routing.value?.rules?.find(r => r.id === id)?.value || ''

  try {
    await api.deleteRoutingRule(id)
    await load()
    emit('notify', `Удалено: ${value}`, 'ok')
  } catch (e: any) {
    emit('notify', e.message)
  }
}

// Выгрузка и загрузка — единственный способ перенести правила на другую
// машину: своего облака у клиента нет и не будет.
function exportRules() {
  if (!routing.value) return

  const count = routing.value.rules?.length || 0
  const body = JSON.stringify({ mode: routing.value.mode, rules: routing.value.rules }, null, 2)

  const url = URL.createObjectURL(new Blob([body], { type: 'application/json' }))
  const a = document.createElement('a')
  a.href = url
  a.download = `awg-routing-${new Date().toISOString().slice(0, 10)}.json`
  a.click()
  URL.revokeObjectURL(url)

  emit('notify', `Выгружено правил: ${count}`, 'ok')
}

function pickFile() {
  fileInput.value?.click()
}

async function onFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]

  // Значение сбрасываем сразу: иначе повторный выбор того же файла не
  // считается изменением и обработчик не вызывается вовсе.
  input.value = ''
  if (!file) return

  try {
    const data = JSON.parse(await file.text())

    if (!data || typeof data !== 'object' || !Array.isArray(data.rules)) {
      throw new Error('В файле нет списка правил')
    }

    await api.setRouting({ mode: data.mode, rules: data.rules })
    // Поиск сбрасываем: иначе загруженные правила остались бы за фильтром,
    // набранным до загрузки.
    search.value = ''
    await load()
    emit('notify', `Загружено правил: ${data.rules.length}`, 'ok')
  } catch (err: any) {
    emit('notify', err instanceof SyntaxError ? 'Файл не является корректным JSON' : err.message)
  }
}

onMounted(load)
</script>
