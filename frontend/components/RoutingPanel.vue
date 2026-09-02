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

    <!-- Одно поле на поиск и на добавление: пока набранное не складывается в
         правило, оно ищет по списку; сложилось — рядом появляется «Добавить».
         Вид правила виден по самому значению, выбирать его руками не нужно. -->
    <div
      class="rule-field"
      :class="{
        'rule-field--bad': draft.kind === 'invalid',
        'rule-field--ready': draft.kind === 'ready' && !existing
      }"
    >
      <svg class="rule-field__icon" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.7" stroke-linecap="round">
        <circle cx="10.5" cy="10.5" r="6.5" />
        <path d="M20 20l-4.6-4.6" />
      </svg>

      <input
        v-model="input"
        type="text"
        class="rule-field__input"
        placeholder="Адрес, подсеть, домен или зона"
        aria-label="Поиск по правилам и добавление нового"
        spellcheck="false"
        autocapitalize="off"
        autocomplete="off"
        @keyup.enter="addRule"
        @keyup.esc="input = ''"
      />

      <span v-if="draft.kind === 'ready'" class="tag rule-field__type">{{ ruleTypeLabel(draft.type!) }}</span>

      <button v-if="input" class="icon-btn rule-field__clear" aria-label="Очистить" @click="input = ''">
        <svg viewBox="0 0 24 24" width="17" height="17" fill="currentColor">
          <path d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm3.5 12.1-1.4 1.4L12 13.4l-2.1 2.1-1.4-1.4L10.6 12 8.5 9.9l1.4-1.4L12 10.6l2.1-2.1 1.4 1.4L13.4 12z" />
        </svg>
      </button>

      <button
        v-if="draft.kind === 'ready' && !existing"
        class="btn btn--accent rule-field__add"
        :disabled="adding"
        @click="addRule"
      >
        <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.9" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
        Добавить
      </button>
    </div>

    <p v-if="hint" class="rule-hint" :class="hintClass">{{ hint }}</p>

    <!-- Правила одного хозяйства стоят вместе: git.dtel.ru, bitrix.dtel.ru и
         адрес того же сервера — это одна запись в голове пользователя. -->
    <div
      v-for="group in visibleGroups"
      :key="group.source"
      class="group"
      :class="`group--${group.color}`"
    >
      <div class="group__head">
        <span class="group__dot" aria-hidden="true"></span>
        <span class="group__name">{{ group.source }}</span>
        <span class="group__count">
          {{ group.visible.length === group.rules.length
            ? group.rules.length
            : `${group.visible.length} из ${group.rules.length}` }}
        </span>
      </div>

      <ul class="list list--tight group__list">
        <RuleRow
          v-for="rule in group.visible"
          :key="rule.id"
          :rule="rule"
          :matched="rule.id === existing?.id"
          @delete="deleteRule"
        />
      </ul>
    </div>

    <ul v-if="visibleLoose.length" class="list list--tight">
      <RuleRow
        v-for="rule in visibleLoose"
        :key="rule.id"
        :rule="rule"
        :matched="rule.id === existing?.id"
        @delete="deleteRule"
      />
    </ul>

    <p v-if="!visibleGroups.length && !visibleLoose.length" class="muted">
      {{ rules.length
        ? 'Ничего не найдено'
        : 'Правил нет. Добавьте адрес (1.1.1.1), подсеть (10.0.0.0/8), домен (dtel.ru) или зону (.ru)' }}
    </p>
  </section>
</template>

<script setup lang="ts">
import type { RoutingConfig, RoutingMode } from '~/composables/useApi'

// Правила маршрутизации: режим, поле поиска-добавления и список.
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

// Источник каждого правила приходит вторым запросом: ответ считается по DNS и
// может задержаться, а список обязан показаться сразу. Пока его нет — список
// просто не сгруппирован.
const sources = ref<Record<string, string>>({})

const input = ref('')
const adding = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)

const modes = [
  { value: 'vpn_list' as RoutingMode, title: 'Только список через VPN', note: 'Остальное идёт напрямую' },
  { value: 'direct_list' as RoutingMode, title: 'Всё через VPN, кроме списка', note: 'Список идёт напрямую' }
]

const rules = computed(() => routing.value?.rules || [])

const draft = computed(() => parseRule(input.value))

// Уже добавленное правило с тем же значением. Сравниваем по приведённому
// виду: «DTEL.RU» и «dtel.ru» — одно и то же, и предлагать добавить второе
// такое же нельзя.
const existing = computed(() => {
  if (draft.value.kind !== 'ready') return null
  return rules.value.find(r => r.value.trim().toLowerCase() === draft.value.value) || null
})

const hint = computed(() => {
  if (draft.value.kind === 'invalid') return draft.value.reason
  if (existing.value) return 'Такое правило уже есть — оно отмечено в списке'
  if (draft.value.kind === 'ready') return 'Enter или «Добавить»'
  return ''
})

const hintClass = computed(() => {
  if (draft.value.kind === 'invalid') return 'rule-hint--bad'
  if (existing.value) return 'rule-hint--known'
  return ''
})

const { groups, loose } = useRuleGroups(rules, sources)

// Поиск идёт по набранному тексту как есть: набрано правило целиком — в
// списке останется оно само, набрано «dtel» — всё хозяйство.
function matches(rule: { type: string; value: string }) {
  const q = input.value.trim().toLowerCase()
  if (!q) return true
  return rule.value.toLowerCase().includes(q) || ruleTypeLabel(rule.type).toLowerCase().includes(q)
}

// Группа остаётся на месте, даже если под фильтр попало одно её правило:
// иначе по найденной записи не понять, к чему она относится.
const visibleGroups = computed(() =>
  groups.value
    .map(group => ({ ...group, visible: group.rules.filter(matches) }))
    .filter(group => group.visible.length > 0)
)

const visibleLoose = computed(() => loose.value.filter(matches))

async function load() {
  try {
    routing.value = await api.getRouting()
  } catch (e: any) {
    emit('notify', e.message)
    return
  }

  loadSources()
}

// Источники подтягиваются отдельно и молча: не ответивший DNS означает список
// без группировки, а не повод ругаться на пользователя.
async function loadSources() {
  try {
    sources.value = await api.getRoutingSources()
  } catch (e) {
    console.error('Не удалось определить источники правил:', e)
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
  if (draft.value.kind !== 'ready' || existing.value || adding.value) return

  const { type, value } = draft.value
  adding.value = true

  try {
    await api.addRoutingRule(type!, value!)
    // Поле очищаем только после согласия службы: отвергнутое значение чаще
    // всего надо поправить, а не набирать заново.
    input.value = ''
    await load()
    emit('notify', `Добавлено: ${value}`, 'ok')
  } catch (e: any) {
    emit('notify', e.message)
  } finally {
    adding.value = false
  }
}

async function deleteRule(id: string) {
  const value = rules.value.find(r => r.id === id)?.value || ''

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
  const target = e.target as HTMLInputElement
  const file = target.files?.[0]

  // Значение сбрасываем сразу: иначе повторный выбор того же файла не
  // считается изменением и обработчик не вызывается вовсе.
  target.value = ''
  if (!file) return

  try {
    const data = JSON.parse(await file.text())

    if (!data || typeof data !== 'object' || !Array.isArray(data.rules)) {
      throw new Error('В файле нет списка правил')
    }

    await api.setRouting({ mode: data.mode, rules: data.rules })
    // Поле сбрасываем: иначе загруженные правила остались бы за фильтром,
    // набранным до загрузки.
    input.value = ''
    await load()
    emit('notify', `Загружено правил: ${data.rules.length}`, 'ok')
  } catch (err: any) {
    emit('notify', err instanceof SyntaxError ? 'Файл не является корректным JSON' : err.message)
  }
}

onMounted(load)
</script>
