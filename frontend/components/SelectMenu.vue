<!--
  Выпадающий список вместо <select>.

  Родной <select> рисует список средствами системы: чужие шрифт, скругления
  и синяя подсветка выбранного пункта. Стилями это не переопределить, поэтому
  список собран из обычных элементов — он подчиняется общему оформлению
  и выделяет выбранный пункт так же, как остальные списки в приложении.
-->
<template>
  <div ref="root" class="sel" :class="{ 'sel--open': open }">
    <button
      type="button"
      class="sel__trigger"
      :aria-expanded="open"
      aria-haspopup="listbox"
      @click="toggle"
      @keydown.down.prevent="openMenu(true)"
      @keydown.up.prevent="openMenu(true)"
    >
      <span class="sel__value">{{ currentLabel }}</span>
      <svg class="sel__chevron" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="3.6" stroke-linecap="round" stroke-linejoin="round">
        <path d="m6 9 6 6 6-6" />
      </svg>
    </button>

    <ul v-if="open" class="sel__menu" role="listbox" @keydown.esc="close">
      <li
        v-for="(opt, i) in options"
        :key="opt.value"
        class="sel__option"
        :class="{
          'sel__option--on': opt.value === modelValue,
          'sel__option--cursor': i === cursor
        }"
        role="option"
        :aria-selected="opt.value === modelValue"
        @click="pick(opt.value)"
        @mouseenter="cursor = i"
      >
        <span class="sel__check">
          <svg v-if="opt.value === modelValue" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="3.6" stroke-linecap="round" stroke-linejoin="round">
            <path d="M20 6 9 17l-5-5" />
          </svg>
        </span>
        {{ opt.label }}
      </li>
    </ul>
  </div>
</template>

<script setup lang="ts">
interface Option {
  value: string
  label: string
}

const props = defineProps<{
  modelValue: string
  options: Option[]
}>()

const emit = defineEmits<{ 'update:modelValue': [string] }>()

const root = ref<HTMLElement | null>(null)
const open = ref(false)
const cursor = ref(0)

const currentLabel = computed(
  () => props.options.find(o => o.value === props.modelValue)?.label ?? ''
)

function openMenu(focusCurrent = false) {
  open.value = true
  cursor.value = focusCurrent
    ? Math.max(0, props.options.findIndex(o => o.value === props.modelValue))
    : 0
}

function close() {
  open.value = false
}

function toggle() {
  open.value ? close() : openMenu(true)
}

function pick(value: string) {
  emit('update:modelValue', value)
  close()
}

function onDocumentDown(e: MouseEvent) {
  if (root.value && !root.value.contains(e.target as Node)) close()
}

function onKey(e: KeyboardEvent) {
  if (!open.value) return

  if (e.key === 'Escape') {
    close()
    return
  }

  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    const step = e.key === 'ArrowDown' ? 1 : -1
    const n = props.options.length
    cursor.value = (cursor.value + step + n) % n
    return
  }

  if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault()
    const opt = props.options[cursor.value]
    if (opt) pick(opt.value)
  }
}

onMounted(() => {
  document.addEventListener('mousedown', onDocumentDown)
  document.addEventListener('keydown', onKey)
})

onUnmounted(() => {
  document.removeEventListener('mousedown', onDocumentDown)
  document.removeEventListener('keydown', onKey)
})
</script>

<style scoped>
.sel {
  position: relative;
}

.sel__trigger {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  padding: 10px 12px;
  background: var(--sunken);
  border: 1px solid var(--line);
  border-radius: var(--r-sm);
  color: var(--text);
  font-family: inherit;
  font-size: 14px;
  cursor: pointer;
  transition: border-color 0.15s ease;
}

.sel__trigger:hover { border-color: #3a4049; }

.sel__trigger:focus-visible {
  outline: none;
  border-color: #4a515c;
  box-shadow: 0 0 0 3px rgba(255, 255, 255, 0.06);
}

.sel--open .sel__trigger { border-color: #4a515c; }

.sel__value {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sel__chevron {
  flex-shrink: 0;
  color: var(--text-dim);
  transition: transform 0.18s ease;
}

.sel--open .sel__chevron { transform: rotate(180deg); }

.sel__menu {
  position: absolute;
  z-index: 30;
  top: calc(100% + 6px);
  left: 0;
  min-width: 100%;
  padding: 5px;
  margin: 0;
  list-style: none;
  background: var(--surface-2);
  border: 1px solid var(--line);
  border-radius: var(--r);
  box-shadow: 0 10px 26px rgba(0, 0, 0, 0.5);
}

.sel__option {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: var(--r-sm);
  font-size: 14px;
  white-space: nowrap;
  cursor: pointer;
  color: var(--text-dim);
}

.sel__option--cursor {
  background: #262a32;
  color: var(--text);
}

.sel__option--on { color: var(--text); }

/* Тот же приём, что и в остальных списках: выбранное помечается
   зелёной галочкой, а не заливкой всей строки. */
.sel__check {
  width: 16px;
  height: 16px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--green-hi);
}
</style>
