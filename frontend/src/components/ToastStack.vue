<template>
  <div class="toasts" aria-live="polite">
    <div v-for="t in toasts" :key="t.id" class="toast" :class="`toast--${t.type}`">
      <span class="toast__tag">{{ t.type === 'ok' ? 'Готово' : 'Ошибка' }}</span>
      <span class="grow">{{ t.text }}<span v-if="t.count > 1" class="faint"> ×{{ t.count }}</span></span>
      <button class="btn btn--ghost btn--sm" aria-label="Закрыть" @click="dismiss(t.id)">×</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useToasts } from '../stores/toasts'

const { toasts, dismiss } = useToasts()
</script>

<style scoped>
.toasts {
  position: fixed;
  right: 16px;
  bottom: 16px;
  z-index: 50;
  display: grid;
  gap: 6px;
  width: min(440px, calc(100vw - 32px));
}

.toast {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 8px 10px;
  border: 1px solid var(--line-strong);
  border-left-width: 2px;
  background: var(--panel);
}

.toast--ok { border-left-color: var(--ok); }
.toast--error { border-left-color: var(--bad); }

.toast__tag {
  font-weight: 600;
}

.toast--ok .toast__tag { color: var(--ok); }
.toast--error .toast__tag { color: var(--bad); }
</style>
