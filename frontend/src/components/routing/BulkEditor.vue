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
    <p class="hint" :class="{ error: plan.bad.length }">{{ hint }}</p>
    <div class="row">
      <button class="btn btn--primary" :class="{ 'btn--busy': saving }" :disabled="!dirty || saving" @click="save">
        <LoaderCircle v-if="saving" class="spin" aria-hidden="true" />
        {{ saving ? 'Применяется…' : dirty ? `Сохранить ${change}` : 'Нет изменений' }}
      </button>
      <button class="btn btn--ghost" :disabled="saving" @click="cancel">Отмена</button>
      <span class="keys"><kbd>Ctrl</kbd>+<kbd>Enter</kbd> — сохранить · <kbd>Esc</kbd> — отмена</span>
    </div>
  </div>
</template>

<script setup lang="ts">
// Правка всех правил текстом. Состояние — в useBulkEdit у родителя.
import { LoaderCircle } from '@lucide/vue'
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
  gap: 10px;
}

.bulk__text {
  min-height: min(420px, 55vh);
}

.hint,
.keys {
  color: var(--muted);
  font-size: 13px;
}

.row {
  flex-wrap: wrap;
}

@media (max-width: 640px) {
  .keys {
    display: none;
  }
}
</style>
