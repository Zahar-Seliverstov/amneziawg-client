<template>
  <div class="adder">
    <div class="adder__box">
      <input
        ref="field"
        v-model="query"
        class="adder__input mono"
        placeholder="youtube.com, 1.2.3.4, 10.0.0.0/8, geosite:youtube, app:steam"
        spellcheck="false"
        autocapitalize="off"
        autocomplete="off"
        aria-label="Добавить правило или найти в списке"
        @keydown="onKey"
        @paste="onPaste"
        @focus="focused = true"
        @blur="focused = false"
      />
      <kbd v-if="!query && !focused" title="Перейти к полю">/</kbd>
      <button
        class="btn btn--primary btn--sm"
        :class="{ 'btn--busy': saving }"
        :disabled="!plan.add.length || saving"
        @click="addAll"
      >{{ plan.add.length > 1 ? `Добавить ${plan.add.length}` : 'Добавить' }}</button>
      <button
        class="btn btn--sm"
        :class="{ 'btn--primary': picking }"
        :aria-expanded="picking"
        title="Выбрать программу из запущенных и установленных"
        @click="picking = !picking"
      >Программа…</button>
    </div>

    <div v-if="picking" class="pop">
      <AppPicker @close="picking = false" />
    </div>

    <ul v-else-if="suggestOpen" class="pop" role="listbox">
      <li
        v-for="(s, i) in suggestions"
        :key="s.text"
        class="suggest"
        :class="{ 'suggest--on': i === cursor }"
        role="option"
        :aria-selected="i === cursor"
        @mousedown.prevent="pick(s)"
        @mousemove="cursor = i"
      >
        <span class="grow ellipsis mono">{{ s.text }}</span>
        <span class="faint">{{ s.meta }}</span>
      </li>
    </ul>

    <p class="hint" :class="{ bad: plan.problem && !plan.add.length }">{{ hint || ' ' }}</p>
  </div>
</template>

<script setup lang="ts">
// Поле добавления правил. Вся логика — в useRuleInput, здесь только вид.
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRuleInput } from '../../composables/useRuleInput'
import AppPicker from './AppPicker.vue'

const field = ref<HTMLInputElement | null>(null)
const picking = ref(false)

const { query, focused, cursor, filter, plan, hint, suggestions, suggestOpen, saving, addAll, pick, onKey, onPaste } =
  useRuleInput(field)

// Текст в поле заодно фильтрует список правил — отдаём фильтр наверх.
defineExpose({ filter })

// «/» из любого места страницы — к полю, Esc — закрыть выбор программы.
function onDocKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    picking.value = false
    return
  }
  const typing = (e.target as HTMLElement).closest('input, textarea, [contenteditable]')
  if (e.key === '/' && !typing) {
    e.preventDefault()
    field.value?.focus()
  }
}

onMounted(() => document.addEventListener('keydown', onDocKey))
onBeforeUnmount(() => document.removeEventListener('keydown', onDocKey))
</script>

<style scoped>
.adder {
  position: relative;
  margin-bottom: 10px;
}

.adder__box {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 38px;
  padding: 0 5px 0 10px;
  border: 1px solid var(--line-strong);
  border-radius: 4px;
  background: var(--bg);
}

.adder__box:focus-within {
  border-color: var(--accent);
}

.adder__input {
  flex: 1;
  min-width: 0;
  height: 100%;
  border: 0;
  background: none;
  outline: none;
}

.adder__input::placeholder {
  color: var(--faint);
}

.hint {
  min-height: 19px;
  margin-top: 4px;
  color: var(--muted);
  font-size: 13px;
}

.pop {
  position: absolute;
  z-index: 10;
  top: 42px;
  left: 0;
  right: 0;
  border: 1px solid var(--line-strong);
  border-radius: 4px;
  background: var(--panel);
  overflow: hidden;
}

.suggest {
  display: flex;
  align-items: center;
  gap: 12px;
  height: 30px;
  padding: 0 10px;
  cursor: pointer;
}

.suggest--on {
  background: var(--sel);
  color: var(--accent);
}
</style>
