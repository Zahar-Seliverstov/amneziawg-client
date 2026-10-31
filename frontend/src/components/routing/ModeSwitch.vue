<template>
  <div class="mode">
    <span class="caption">Через VPN идёт</span>
    <div class="seg" role="radiogroup" aria-label="Режим маршрутизации">
      <button
        v-for="option in MODES"
        :key="option.value"
        type="button"
        role="radio"
        class="seg__btn"
        :class="{ 'seg__btn--on': mode === option.value }"
        :aria-checked="mode === option.value"
        :disabled="switching"
        @click="setMode(option.value)"
      >{{ option.title }}</button>
    </div>
    <p class="note">{{ current?.note }}</p>
  </div>
</template>

<script setup lang="ts">
// Режим маршрутизации: что идёт в туннель — список или всё, кроме него.
// Применяется сразу, без кнопки «сохранить».
import { computed } from 'vue'
import type { RoutingMode } from '../../api/types'
import { useRouting } from '../../stores/routing'

const MODES: { value: RoutingMode; title: string; note: string }[] = [
  { value: 'vpn_list', title: 'только список', note: 'Правила ниже — через VPN. Остальное идёт напрямую, как без VPN.' },
  { value: 'direct_list', title: 'всё, кроме списка', note: 'Всё идёт через VPN. Правила ниже — напрямую, мимо туннеля.' }
]

const { mode, switching, setMode } = useRouting()
const current = computed(() => MODES.find(m => m.value === mode.value))
</script>

<style scoped>
.mode {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 12px;
  margin-bottom: 14px;
}

.seg {
  display: inline-flex;
  border: 1px solid var(--line-strong);
  border-radius: 4px;
  overflow: hidden;
}

.seg__btn {
  height: 28px;
  padding: 0 12px;
  color: var(--muted);
}

.seg__btn + .seg__btn {
  border-left: 1px solid var(--line-strong);
}

.seg__btn:hover:not(:disabled) {
  color: var(--text);
}

.seg__btn--on {
  background: var(--sel);
  color: var(--accent);
  font-weight: 600;
}

.note {
  flex-basis: 100%;
  color: var(--muted);
  font-size: 13px;
}
</style>
