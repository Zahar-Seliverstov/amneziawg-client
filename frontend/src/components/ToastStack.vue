<template>
  <TransitionGroup tag="div" name="toast" class="toasts" aria-live="polite" aria-relevant="additions">
    <div v-for="t in toasts" :key="t.id" class="toast" :role="t.type === 'error' ? 'alert' : 'status'">
      <span class="grow">{{ t.text }}<span v-if="t.count > 1" class="toast__count num"> ×{{ t.count }}</span></span>
      <button class="toast__close" aria-label="Закрыть уведомление" @click="dismiss(t.id)">
        <X aria-hidden="true" />
      </button>
    </div>
  </TransitionGroup>
</template>

<script setup lang="ts">
// Уведомления — пилюля внизу по центру, в цвете, обратном странице: её
// видно и без цветных значков, а ошибка отличается словами.
import { X } from '@lucide/vue'
import { useToasts } from '../stores/toasts'

const { toasts, dismiss } = useToasts()
</script>

<style scoped>
.toasts {
  position: fixed;
  left: 50%;
  bottom: 20px;
  z-index: 50;
  display: grid;
  justify-items: center;
  gap: 8px;
  width: min(480px, calc(100vw - 24px));
  transform: translateX(-50%);
  pointer-events: none;
}

.toast {
  display: flex;
  align-items: center;
  gap: 8px;
  max-width: 100%;
  min-height: 40px;
  padding: 8px 8px 8px 16px;
  border-radius: 20px;
  background: var(--text);
  color: var(--bg);
  font-size: 13px;
  line-height: 1.4;
  pointer-events: auto;
}

.toast__count {
  opacity: 0.6;
}

.toast__close {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  flex: none;
  border-radius: 50%;
  opacity: 0.6;
  transition: opacity var(--t);
}

.toast__close:hover {
  opacity: 1;
}

.toast__close svg {
  width: 14px;
  height: 14px;
}

.toast-enter-active,
.toast-leave-active {
  transition: opacity var(--t);
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
}
</style>
