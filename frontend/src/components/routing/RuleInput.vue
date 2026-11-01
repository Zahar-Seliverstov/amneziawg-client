<template>
  <div class="adder">
    <div class="field-box adder__box">
      <Search aria-hidden="true" />
      <input
        ref="field"
        v-model="query"
        placeholder="Добавить или найти: youtube.com, geoip:ru, app:steam"
        spellcheck="false"
        autocapitalize="off"
        autocomplete="off"
        aria-label="Добавить правило или найти в списке"
        :aria-describedby="hintId"
        @keydown="onKey"
        @paste="onPaste"
        @focus="focused = true"
        @blur="focused = false"
      />
      <button v-if="query" type="button" class="btn btn--ghost btn--icon btn--sm" aria-label="Очистить поле" title="Очистить · Esc" @click="clear">
        <X aria-hidden="true" />
      </button>
      <kbd v-else-if="!focused" class="adder__kbd" title="Перейти к полю">/</kbd>
      <!-- «Добавить» появляется, только когда есть что добавить: в остальное
           время поле — просто поиск, и лишняя кнопка в нём шумит. -->
      <button
        v-if="plan.add.length || saving"
        type="button"
        class="btn btn--primary btn--sm adder__add"
        :class="{ 'btn--busy': saving }"
        :disabled="saving"
        :title="saving ? 'Применяется…' : 'Добавить · Enter'"
        @click="addAll"
      >
        <LoaderCircle v-if="saving" class="spin" aria-hidden="true" />
        {{ saving ? 'Применяется…' : plan.add.length > 1 ? `Добавить ${plan.add.length}` : 'Добавить' }}
      </button>
      <button
        type="button"
        class="btn btn--ghost btn--icon btn--sm"
        :class="{ 'btn--on': picking }"
        :aria-expanded="picking"
        aria-label="Выбрать программу"
        title="Выбрать программу из запущенных и установленных"
        @click="picking = !picking"
      >
        <AppWindow aria-hidden="true" />
      </button>
    </div>

    <Transition name="pop">
      <div v-if="picking" class="pop adder__pop">
        <AppPicker @close="closePicker" />
      </div>

      <ul v-else-if="suggestOpen" class="pop adder__pop adder__suggest" role="listbox" aria-label="Категории из баз">
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
          <span class="grow ellipsis">{{ s.text }}</span>
          <span class="faint num">{{ s.meta }}</span>
        </li>
      </ul>
    </Transition>

    <p :id="hintId" class="hint" :class="{ error: plan.problem && !plan.add.length }">{{ hint || '\u00a0' }}</p>
  </div>
</template>

<script setup lang="ts">
// Поле добавления правил. Вся логика — в useRuleInput, здесь только вид.
import { AppWindow, LoaderCircle, Search, X } from '@lucide/vue'
import { nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { usePage } from '../../composables/usePage'
import { useRuleInput } from '../../composables/useRuleInput'
import AppPicker from './AppPicker.vue'

const field = ref<HTMLInputElement | null>(null)
const picking = ref(false)
const hintId = useId()
const { open } = usePage()

const { query, focused, cursor, filter, plan, hint, suggestions, suggestOpen, saving, addAll, pick, onKey, onPaste } =
  useRuleInput(field)

// Текст в поле заодно фильтрует список правил — отдаём фильтр наверх.
defineExpose({ filter })

function clear() {
  query.value = ''
  field.value?.focus()
}

function closePicker() {
  picking.value = false
  nextTick(() => field.value?.focus())
}

// «/» из любого места страницы — к полю (из настроек тоже: сперва назад),
// Esc — закрыть выбор программы.
function onDocKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    if (picking.value) closePicker()
    return
  }
  const typing = (e.target as HTMLElement).closest('input, textarea, [contenteditable]')
  if (e.key === '/' && !typing) {
    e.preventDefault()
    open('main')
    nextTick(() => field.value?.focus())
  }
}

onMounted(() => document.addEventListener('keydown', onDocKey))
onBeforeUnmount(() => document.removeEventListener('keydown', onDocKey))
</script>

<style scoped>
.adder {
  position: relative;
}

.adder__box {
  height: 40px;
  padding: 0 6px 0 12px;
}

.adder__box > input {
  margin-left: 2px;
}

.adder__kbd {
  margin-right: 4px;
}

.adder__add {
  height: 28px;
}

.adder__pop {
  top: 46px;
  left: 0;
  right: 0;
}

.adder__suggest {
  padding: 4px;
}

.hint {
  min-height: 20px;
  margin: 8px 0 4px;
  color: var(--muted);
  font-size: 13px;
}

.suggest {
  display: flex;
  align-items: center;
  gap: 12px;
  height: 34px;
  padding: 0 10px;
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
}

.suggest--on {
  background: var(--surface);
}

@media (max-width: 640px) {
  .adder__kbd {
    display: none;
  }
}
</style>
