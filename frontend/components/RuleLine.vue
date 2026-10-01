<template>
  <li class="line" :class="[`line--${mark.kind}`, { 'line--tied': tied }]">
    <input
      ref="field"
      class="line__input"
      :value="text"
      :placeholder="placeholder"
      spellcheck="false"
      autocapitalize="off"
      autocomplete="off"
      aria-label="Правило маршрутизации"
      @input="$emit('update', ($event.target as HTMLInputElement).value)"
      @keydown.enter.prevent="$emit('split', caret())"
      @keydown.backspace="onBackspace"
      @keydown.up.prevent="$emit('move', -1)"
      @keydown.down.prevent="$emit('move', 1)"
      @paste="onPaste"
    />

    <!-- Напротив строки — то, чем она станет. Вид правила выводится из самого
         значения, поэтому выбирать его руками не нужно; а «ошибка» и «уже
         есть» стоят на том же месте, что и вид, потому что отвечают на тот же
         вопрос: чем эта строка будет в списке. -->
    <span class="type line__mark" :class="mark.type && `type--${mark.type}`" :title="mark.reason">
      {{ mark.label }}
    </span>

    <button
      v-if="text"
      class="icon-btn icon-btn--danger line__drop"
      aria-label="Убрать строку"
      tabindex="-1"
      @click="$emit('drop')"
    >
      <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.9" stroke-linecap="round">
        <path d="M18 6 6 18M6 6l12 12" />
      </svg>
    </button>
  </li>
</template>

<script setup lang="ts">
// Одна строка редактора правил.
//
// Компонент намеренно ничего не решает: он не знает ни про список, ни про
// службу, ни про то, годится ли набранное. Разбор и порядок строк — забота
// панели, здесь только ввод и события о том, что с ним сделали. Иначе
// переходы фокуса между строками пришлось бы согласовывать в двух местах.

// mark — что показать напротив: вид правила, «ошибка» с причиной или
// «уже есть». Приходит готовым: одна и та же строка считается и для метки,
// и для разницы с сохранённым списком, и считать её дважды незачем.
export interface LineMark {
  kind: 'empty' | 'ok' | 'bad' | 'dup'
  label: string
  // type — вид правила: от него цвет метки. Тот же набор цветов, что и в
  // прежнем списке: адрес, подсеть, домен и зона различаются на глаз.
  type?: string
  reason?: string
}

const props = defineProps<{
  text: string
  mark: LineMark
  placeholder?: string
  // tied — соседняя строка относится к тому же хозяйству. Полоса слева
  // показывает это, не разрывая список заголовками.
  tied?: boolean
}>()

const emit = defineEmits<{
  update: [text: string]
  // split — Enter: разорвать строку в этом месте и перейти на новую.
  split: [at: number]
  // join — Backspace в начале пустой строки: убрать её и вернуться назад.
  join: []
  drop: []
  move: [delta: number]
  // paste — вставили несколько строк: панель разложит их по строкам сама.
  paste: [lines: string[], at: number, to: number]
}>()

// Ссылка на поле нужна только ради положения курсора: где разорвать строку
// при Enter и куда легла вставка. Фокус переставляет панель — только она
// знает, какая строка появилась и какая исчезла.
const field = ref<HTMLInputElement | null>(null)

function caret() {
  return field.value?.selectionStart ?? props.text.length
}

// Backspace в начале строки склеивает её с предыдущей — как в любом
// текстовом поле. Только в начале и только без выделения: иначе он должен
// стирать символ, а не строку.
function onBackspace(e: KeyboardEvent) {
  const input = field.value
  if (!input || input.selectionStart !== 0 || input.selectionEnd !== 0) return

  e.preventDefault()
  emit('join')
}

// Вставка списка из буфера — главный способ завести правила пачкой. Перевод
// строки в однострочном поле пропал бы молча, поэтому разбираем вставку сами.
function onPaste(e: ClipboardEvent) {
  const text = e.clipboardData?.getData('text') || ''
  if (!text.includes('\n')) return

  e.preventDefault()

  const input = field.value
  const from = input?.selectionStart ?? props.text.length
  const to = input?.selectionEnd ?? from

  emit('paste', text.split(/\r?\n/), from, to)
}

</script>
