<template>
  <div class="bulk">
    <textarea
      ref="area"
      v-model="draft"
      class="input bulk__text"
      spellcheck="false"
      aria-label="Правила списком"
      @keydown.ctrl.enter.prevent="dirty && save()"
      @keydown.esc="cancel"
    ></textarea>
    <p class="hint" :class="{ bad: plan.bad.length }">{{ hint }}</p>
    <div class="row">
      <button class="btn btn--primary" :class="{ 'btn--busy': saving }" :disabled="!dirty || saving" @click="save">
        {{ dirty ? `Сохранить ${change}` : 'Нет изменений' }}
      </button>
      <button class="btn btn--ghost" :disabled="saving" @click="cancel">Отмена</button>
      <span class="faint keys">Ctrl+Enter — сохранить · Esc — отмена</span>
    </div>
  </div>
</template>

<script setup lang="ts">
// Правка всех правил текстом. Состояние — в useBulkEdit у родителя.
import { nextTick, onMounted, ref } from 'vue'
import type { useBulkEdit } from '../../composables/useBulkEdit'

const props = defineProps<{ editor: ReturnType<typeof useBulkEdit> }>()
const { draft, plan, dirty, change, hint, saving, save, cancel } = props.editor

const area = ref<HTMLTextAreaElement | null>(null)

// Курсор — в конец: дописывают чаще, чем правят середину.
onMounted(() =>
  nextTick(() => {
    const el = area.value
    if (!el) return
    el.focus()
    el.setSelectionRange(el.value.length, el.value.length)
    el.scrollTop = el.scrollHeight
  })
)
</script>

<style scoped>
.bulk {
  display: grid;
  gap: 8px;
}

.bulk__text {
  min-height: 420px;
}

.hint,
.keys {
  color: var(--muted);
  font-size: 13px;
}
</style>
