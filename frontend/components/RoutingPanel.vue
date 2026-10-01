<template>
  <section class="section">
    <div class="section__head">
      <span class="hint">
        {{ dirty ? 'Изменения ещё не применены' : 'Режим применяется сразу, правила — по кнопке' }}
      </span>
      <span class="section__tools">
        <button
          v-if="canOrder"
          class="btn btn--quiet"
          title="Поставить рядом правила одного хозяйства"
          @click="orderBySource"
        >
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="3.1" stroke-linecap="round" stroke-linejoin="round">
            <path d="M4 7h10M4 12h16M4 17h7" />
          </svg>
          Упорядочить
        </button>
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
        <input ref="fileInput" type="file" accept="application/json,.json" hidden @change="onFile" />
      </span>
    </div>

    <div class="choice" :class="{ 'choice--pending': switching }">
      <label
        v-for="option in modes"
        :key="option.value"
        class="choice__item"
        :class="{ 'choice__item--on': routing?.mode === option.value }"
      >
        <input
          type="radio"
          name="mode"
          :value="option.value"
          :checked="routing?.mode === option.value"
          @change="setMode(option.value)"
        />
        <span class="mark mark--radio">
          <svg v-if="routing?.mode === option.value" viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="3.3" stroke-linecap="round" stroke-linejoin="round">
            <path d="M20 6 9 17l-5-5" />
          </svg>
        </span>
        <span>
          <span class="choice__title">{{ option.title }}</span>
          <span class="choice__note">{{ option.note }}</span>
        </span>
      </label>
    </div>

    <!-- Разобранный файл ждёт решения. В поле он ложится текстом и в силу
         сам по себе не вступает: увидев результат, его ещё можно поправить
         или отменить, не тронув работающую маршрутизацию. -->
    <Transition name="fade">
      <div v-if="pending" class="pending">
        <p class="pending__text">
          В файле {{ pending.name }} — правил: {{ pending.values.length }}.
          Добавить их к тому, что в поле, или заменить ими всё?
        </p>
        <div class="pending__actions">
          <button class="btn btn--accent" @click="takeFile(false)">Добавить к текущим</button>
          <button class="btn btn--danger" @click="takeFile(true)">Заменить всё</button>
          <button class="btn btn--quiet" @click="pending = null">Отмена</button>
        </div>
      </div>
    </Transition>

    <!-- Список правил и есть поле ввода: по правилу на строку, напротив
         каждой — чем она станет. Отдельного поля для добавления нет и
         крестиков для удаления по одному тоже: правило заводится набором
         новой строки, а убирается стиранием своей. -->
    <TransitionGroup tag="ul" name="list" class="lines" v-bind="collapse">
      <RuleLine
        v-for="(line, i) in lines"
        :key="line.key"
        :text="line.text"
        :mark="marks[i]"
        :tied="ties[i]"
        :placeholder="i === lines.length - 1 ? 'Адрес, подсеть, домен или зона' : ''"
        @update="text => edit(i, text)"
        @split="at => split(i, at)"
        @join="join(i)"
        @drop="drop(i)"
        @move="delta => move(i, delta)"
        @paste="(chunks, from, to) => paste(i, chunks, from, to)"
      />
    </TransitionGroup>

    <Transition name="fade">
      <p v-if="badCount" class="rule-hint rule-hint--bad">
        Строк с ошибкой: {{ badCount }} — они останутся в поле, но в силу не вступят
      </p>
    </Transition>

    <!-- Строка применения. Пока она не нажата, служба ничего не знает о
         правках: набор правил меняется целиком и разом, а не по букве. -->
    <div class="apply">
      <span class="apply__count">{{ summary }}</span>
      <button
        class="btn btn--accent"
        :class="{ 'btn--busy': applying }"
        :disabled="!dirty || applying"
        @click="apply"
      >
        <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.9" stroke-linecap="round" stroke-linejoin="round">
          <path d="M20 6 9 17l-5-5" />
        </svg>
        {{ dirty ? `Применить: ${change}` : 'Применить' }}
      </button>
      <button v-if="dirty" class="btn btn--quiet" :disabled="applying" @click="reset">
        Отменить правки
      </button>
    </div>
  </section>
</template>

<script setup lang="ts">
import type { RoutingConfig, RoutingMode, RoutingRule } from '~/composables/useApi'
import type { LineMark } from './RuleLine.vue'

// Правила маршрутизации: режим и редактор списка.
//
// Список правил и есть поле ввода — по правилу на строку. Раньше здесь было
// три способа делать одно и то же: поле «поиск и добавление», список со
// строками-крестиками и отдельное окно для ввода списком. Теперь способ один,
// и он же самый быстрый: правило заводится набором строки, убирается её
// стиранием, а сотня правил вставляется из буфера.
//
// Правки копятся и уходят одним запросом по «Применить». Служба на каждое
// применение пересобирает маршрутизацию на живом туннеле, поэтому набор
// правил должен меняться разом, а не по букве; заодно видно заранее, что
// именно изменится, и до нажатия ничего не потеряно.
const emit = defineEmits<{ notify: [text: string, type?: 'error' | 'ok'] }>()

const api = useApi()

const routing = ref<RoutingConfig | null>(null)

// Источник каждого правила приходит вторым запросом: ответ считается по DNS и
// может задержаться, а список обязан показаться сразу. Нужен для полосы
// хозяйства слева и для кнопки «Упорядочить».
const sources = ref<Record<string, string>>({})

// line — одна строка редактора. Ключ свой, не зависящий от текста: строку
// правят посимвольно, и ключ по значению заставлял бы Vue пересоздавать поле
// на каждую букву — вместе с потерей фокуса.
interface Line {
  key: number
  text: string
  // id — правило в службе, из которого выросла строка. Пусто у набранных
  // руками: у них идентификатора ещё нет.
  id?: string
}

let nextKey = 1

// seeded — поле уже заполняли из службы. До этого любое сравнение с
// сохранённым списком бессмысленно: пустое поле означает не «удалить всё», а
// «ещё не загрузились».
let seeded = false

const lines = ref<Line[]>([])
const applying = ref(false)
const switching = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)

const modes = [
  { value: 'vpn_list' as RoutingMode, title: 'Только список через VPN', note: 'Остальное идёт напрямую' },
  { value: 'direct_list' as RoutingMode, title: 'Всё через VPN, кроме списка', note: 'Список идёт напрямую' }
]

const saved = computed(() => routing.value?.rules || [])

// Разбор строк. Считается один раз на всё: и метка напротив строки, и
// разница с сохранённым списком берутся отсюда.
interface Parsed {
  mark: LineMark
  // key — правило в виде «вид:значение». Пусто у строк, которые правилом не
  // станут: пустых, ошибочных и повторов.
  key: string
  value: string
  type?: string
}

const parsed = computed<Parsed[]>(() => {
  const seen = new Set<string>()

  return lines.value.map(line => {
    const draft = parseRule(line.text)

    if (draft.kind === 'empty') {
      return { mark: { kind: 'empty', label: '' }, key: '', value: '' }
    }

    if (draft.kind !== 'ready') {
      // «Слово без точки» здесь не поиск, а опечатка: искать в редактируемом
      // списке нечего, а домен верхнего уровня пишется зоной.
      const reason = draft.kind === 'query'
        ? 'домен верхнего уровня пишется зоной: .ru'
        : draft.reason || 'не похоже на правило'

      return { mark: { kind: 'bad', label: 'Ошибка', reason }, key: '', value: line.text.trim() }
    }

    const value = draft.value!
    const key = `${draft.type}:${value}`

    if (seen.has(key)) {
      return {
        mark: { kind: 'dup', label: 'Уже есть', reason: 'такое правило стоит выше' },
        key: '',
        value,
        type: draft.type
      }
    }
    seen.add(key)

    return {
      mark: { kind: 'ok', label: ruleTypeLabel(draft.type!), type: draft.type },
      key,
      value,
      type: draft.type
    }
  })
})

const marks = computed(() => parsed.value.map(p => p.mark))
const badCount = computed(() => parsed.value.filter(p => p.mark.kind === 'bad').length)

// Разница с тем, что записано в службе. Считается по значениям, а не по
// строкам: переставленные местами правила — это не правка.
const diff = computed(() => {
  const before = new Map<string, string>()
  for (const rule of saved.value) {
    before.set(`${rule.type}:${rule.value.trim().toLowerCase()}`, rule.id)
  }

  const after = new Set(parsed.value.map(p => p.key).filter(Boolean))

  const add: string[] = []
  for (const item of parsed.value) {
    if (item.key && !before.has(item.key)) add.push(item.value)
  }

  const remove: string[] = []
  for (const [key, id] of before) {
    if (!after.has(key)) remove.push(id)
  }

  return { add, remove }
})

const dirty = computed(() => diff.value.add.length > 0 || diff.value.remove.length > 0)

const change = computed(() => {
  const { add, remove } = diff.value
  const parts: string[] = []
  if (add.length) parts.push(`+${add.length}`)
  if (remove.length) parts.push(`−${remove.length}`)
  return parts.join(' ')
})

const summary = computed(() => {
  const { add, remove } = diff.value
  if (!add.length && !remove.length) return `Правил: ${saved.value.length}`

  const parts: string[] = []
  if (add.length) parts.push(`добавится ${add.length}`)
  if (remove.length) parts.push(`удалится ${remove.length}`)
  return parts.join(', ')
})

// Хозяйство: git.dtel.ru, bitrix.dtel.ru и адрес того же сервера — одна
// запись в голове пользователя. Заголовка у группы нет, её держит полоса
// слева у соседних строк одного источника.
function sourceOf(line: Line) {
  return line.id ? sources.value[line.id] || '' : ''
}

const ties = computed(() =>
  lines.value.map((line, i) => {
    const source = sourceOf(line)
    if (!source) return false

    const before = i > 0 && sourceOf(lines.value[i - 1]) === source
    const after = i < lines.value.length - 1 && sourceOf(lines.value[i + 1]) === source
    return before || after
  })
)

// Упорядочивать есть что, только когда хотя бы одно хозяйство разбросано по
// списку: кнопка, которая ничего не изменит, вводит в заблуждение.
const canOrder = computed(() => {
  const order: string[] = []
  for (const line of lines.value) {
    const source = sourceOf(line)
    if (!source) continue
    if (order[order.length - 1] !== source) order.push(source)
  }
  return new Set(order).size !== order.length
})

// Правила одного хозяйства собираются к первому из них. Порядок остального не
// трогаем: список набирал пользователь, и перетасовывать его целиком ради
// группировки — больше потеря, чем польза.
function orderBySource() {
  const rest = [...lines.value]
  const result: Line[] = []

  while (rest.length) {
    const line = rest.shift()!
    result.push(line)

    const source = sourceOf(line)
    if (!source) continue

    // Идём с конца, чтобы splice не сбивал ещё не просмотренные места, а
    // собранное разворачиваем обратно: внутри хозяйства порядок должен
    // остаться тем, который набрал пользователь.
    const same: Line[] = []
    for (let i = rest.length - 1; i >= 0; i--) {
      if (sourceOf(rest[i]) === source) same.unshift(...rest.splice(i, 1))
    }
    result.push(...same)
  }

  lines.value = result
}

// ─── Правка строк ───────────────────────────────────────────────────────
//
// Фокус переставляет панель, а не сама строка: только здесь известно, какая
// строка появилась и какая исчезла.

// Ищем поле в разметке, а не держим карту ссылок: строки вставляются и
// убираются посреди списка, и карта, ключом которой служит номер строки,
// разъезжается с ним ровно там, где нужна.
function focusLine(i: number, at?: number) {
  nextTick(() => {
    const input = document.querySelectorAll<HTMLInputElement>('.lines .line__input')[i]
    if (!input) return

    input.focus()
    const pos = at ?? input.value.length
    input.setSelectionRange(pos, pos)
  })
}

function edit(i: number, text: string) {
  lines.value[i] = { ...lines.value[i], text }
  keepTrailingLine()
}

// Enter разрывает строку в месте курсора: хвост уезжает на новую. Правило,
// приписанное к строке, при этом теряется — от него осталась только часть.
function split(i: number, at: number) {
  const line = lines.value[i]

  // Внизу и так стоит пустая строка — вторая такая же ни к чему.
  if (!line.text && i === lines.value.length - 1) return

  const head = line.text.slice(0, at)
  const tail = line.text.slice(at)

  lines.value.splice(i, 1,
    { key: line.key, text: head, id: head === line.text ? line.id : undefined },
    { key: nextKey++, text: tail })

  keepTrailingLine()
  focusLine(i + 1, 0)
}

// Backspace в начале строки склеивает её с предыдущей — как в любом
// текстовом поле.
function join(i: number) {
  if (i === 0) return

  const previous = lines.value[i - 1]
  const at = previous.text.length

  lines.value.splice(i - 1, 2, {
    key: previous.key,
    text: previous.text + lines.value[i].text,
    id: lines.value[i].text ? undefined : previous.id
  })

  keepTrailingLine()
  focusLine(i - 1, at)
}

function drop(i: number) {
  lines.value.splice(i, 1)
  keepTrailingLine()
  focusLine(Math.max(0, i - 1))
}

function move(i: number, delta: number) {
  const target = i + delta
  if (target >= 0 && target < lines.value.length) focusLine(target)
}

// Вставка списка из буфера — главный способ завести правила пачкой. Строки
// раскладываются по строкам редактора, а начало и хвост текущей остаются на
// своих местах.
function paste(i: number, chunks: string[], from: number, to: number) {
  const line = lines.value[i]
  const head = line.text.slice(0, from)
  const tail = line.text.slice(to)

  const texts = chunks.map(chunk => chunk.trim())
  texts[0] = head + texts[0]
  texts[texts.length - 1] += tail

  const fresh = texts.map((text, n) => ({
    key: n === 0 ? line.key : nextKey++,
    text,
    id: undefined
  }))

  lines.value.splice(i, 1, ...fresh)
  keepTrailingLine()
  focusLine(i + fresh.length - 1)
}

// Снизу всегда одна пустая строка: иначе набрать новое правило было бы негде,
// а заводить её кнопкой — лишний шаг там, где хватает курсора.
function keepTrailingLine() {
  const last = lines.value[lines.value.length - 1]
  if (!last || last.text.trim()) lines.value.push({ key: nextKey++, text: '' })
}

// ─── Служба ─────────────────────────────────────────────────────────────

function seed(rules: RoutingRule[]) {
  lines.value = rules.map(rule => ({ key: nextKey++, text: rule.value, id: rule.id }))
  keepTrailingLine()
  seeded = true
}

async function load() {
  try {
    routing.value = await api.getRouting()
  } catch (e: any) {
    emit('notify', e.message)
    return
  }

  // Непринятые правки не затираем: перечитать список могли и по своим
  // причинам, а набранное пользователем принадлежит ему. На первой загрузке
  // затирать нечего — поле ещё пустое, а пустое поле против непустого списка
  // выглядит как «удалить всё».
  if (!seeded || !dirty.value) seed(saved.value)

  loadSources()
}

let sourcesWarned = false

async function loadSources() {
  try {
    sources.value = await api.getRoutingSources()
  } catch (e: any) {
    console.error('Не удалось определить источники правил:', e)

    if (!sourcesWarned) {
      sourcesWarned = true
      emit('notify', 'Служба не умеет группировать правила — пересоберите и перезапустите её')
    }
  }
}

async function setMode(mode: RoutingMode) {
  switching.value = true

  try {
    await api.setRoutingMode(mode)
    if (routing.value) routing.value = { ...routing.value, mode }
  } catch (e: any) {
    emit('notify', e.message)
  }

  switching.value = false
}

// Весь набор правок уходит одним запросом: служба применит его разом и
// пересоберёт маршруты один раз, а не по разу на правило.
async function apply() {
  if (!dirty.value || applying.value) return

  const { add, remove } = diff.value
  applying.value = true

  try {
    const result = await api.batchRoutingRules(add, remove)

    routing.value = await api.getRouting()
    seed(saved.value)
    loadSources()

    const parts: string[] = []
    if (result.added.length) parts.push(`добавлено ${result.added.length}`)
    if (result.removed) parts.push(`удалено ${result.removed}`)
    if (!parts.length) parts.push('изменений нет')

    // Об отвергнутом говорим отдельно и поимённо: молчать о нём значило бы
    // показывать список, которого в службе нет.
    if (result.rejected.length) {
      const names = result.rejected.slice(0, 3).map(r => r.value).join(', ')
      const tail = result.rejected.length > 3 ? ` и ещё ${result.rejected.length - 3}` : ''
      emit('notify', `Не взяли: ${names}${tail}`)
    } else {
      emit('notify', parts.join(', '), 'ok')
    }
  } catch (e: any) {
    // Пакетная правка появилась вместе с этим окном. Служба постарше о ней
    // не знает, и «Ошибка 404» тут ничего не объясняет.
    emit('notify', /404/.test(e.message)
      ? 'Служба не умеет применять правила пачкой — пересоберите и перезапустите её'
      : e.message)
  } finally {
    applying.value = false
  }
}

function reset() {
  seed(saved.value)
}

// ─── Файлы ──────────────────────────────────────────────────────────────
//
// Выгрузка и загрузка — единственный способ перенести правила на другую
// машину: своего облака у клиента нет и не будет.

function exportRules() {
  if (!routing.value) return

  const body = JSON.stringify({ mode: routing.value.mode, rules: saved.value }, null, 2)

  const url = URL.createObjectURL(new Blob([body], { type: 'application/json' }))
  const a = document.createElement('a')
  a.href = url
  a.download = `awg-routing-${new Date().toISOString().slice(0, 10)}.json`
  a.click()
  URL.revokeObjectURL(url)

  emit('notify', `Выгружено правил: ${saved.value.length}`, 'ok')
}

function pickFile() {
  fileInput.value?.click()
}

interface PendingImport {
  name: string
  mode: RoutingMode | null
  values: string[]
}

const pending = ref<PendingImport | null>(null)

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

    const values = data.rules
      .map((r: any) => (typeof r === 'string' ? r : r?.value))
      .filter((v: any): v is string => typeof v === 'string' && v.trim() !== '')
      .map((v: string) => v.trim())

    if (!values.length) throw new Error('В файле нет ни одного правила')

    // Спрашиваем, а не решаем за пользователя: раньше загрузка молча
    // затирала весь список, а сообщение говорило только про загруженное.
    pending.value = {
      name: file.name,
      mode: data.mode === 'vpn_list' || data.mode === 'direct_list' ? data.mode : null,
      values
    }
  } catch (err: any) {
    emit('notify', err instanceof SyntaxError ? 'Файл не является корректным JSON' : err.message)
  }
}

// Файл ложится в поле текстом и в силу сам по себе не вступает: увидев
// результат, его ещё можно поправить или отменить.
async function takeFile(replace: boolean) {
  const chosen = pending.value
  if (!chosen) return

  const fresh = chosen.values.map(text => ({ key: nextKey++, text, id: undefined }))
  lines.value = replace ? fresh : [...lines.value.filter(line => line.text.trim()), ...fresh]
  keepTrailingLine()

  pending.value = null

  // Режим — не правило, он применяется сразу, как и переключателем выше.
  if (chosen.mode && chosen.mode !== routing.value?.mode) {
    await setMode(chosen.mode)
  }

  emit('notify', `В поле добавлено строк: ${chosen.values.length} — нажмите «Применить»`, 'ok')
}

onMounted(load)
</script>
