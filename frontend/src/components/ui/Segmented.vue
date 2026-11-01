<template>
  <div class="seg" role="radiogroup" :aria-label="label">
    <button
      v-for="(option, i) in options"
      :key="option.value"
      ref="buttons"
      type="button"
      role="radio"
      class="seg__btn"
      :class="{ 'seg__btn--on': option.value === modelValue }"
      :aria-checked="option.value === modelValue"
      :tabindex="option.value === modelValue || (!hasValue && i === 0) ? 0 : -1"
      :disabled="disabled"
      @click="choose(option.value)"
      @keydown="onKey($event, i)"
    >
      <component :is="option.icon" v-if="option.icon" aria-hidden="true" />
      <span>{{ option.title }}</span>
    </button>
  </div>
</template>

<script setup lang="ts" generic="T extends string">
// Переключатель из нескольких вариантов — режим маршрутизации, тема.
// Ведёт себя как группа радиокнопок: стрелки двигают выбор, Tab уходит
// из группы целиком.
import { computed, ref, type Component } from 'vue'

export interface SegmentedOption<V extends string> {
  value: V
  title: string
  icon?: Component
}

const props = defineProps<{
  options: SegmentedOption<T>[]
  modelValue: T | undefined
  label: string
  disabled?: boolean
}>()

const emit = defineEmits<{ 'update:modelValue': [value: T] }>()

const buttons = ref<HTMLButtonElement[]>([])
const hasValue = computed(() => props.options.some(o => o.value === props.modelValue))

function choose(value: T) {
  if (value !== props.modelValue) emit('update:modelValue', value)
}

function onKey(e: KeyboardEvent, i: number) {
  const step = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0
  if (!step) return
  e.preventDefault()
  const n = props.options.length
  const next = (i + step + n) % n
  buttons.value[next]?.focus()
  choose(props.options[next]!.value)
}
</script>

<style scoped>
.seg {
  display: inline-flex;
  max-width: 100%;
  padding: 2px;
  gap: 2px;
  border-radius: var(--r);
  background: var(--surface);
}

.seg__btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-width: 0;
  height: 28px;
  padding: 0 12px;
  border-radius: 6px;
  color: var(--muted);
  font-size: 13px;
  white-space: nowrap;
  transition: background-color var(--t), color var(--t);
}

.seg__btn svg {
  width: 14px;
  height: 14px;
}

.seg__btn:hover:not(:disabled) {
  color: var(--text);
}

/* Выбранный вариант — «вырез» цвета страницы в подложке, без тени. */
.seg__btn--on {
  background: var(--bg);
  color: var(--text);
  font-weight: 500;
}

.seg__btn:disabled {
  cursor: progress;
}
</style>
