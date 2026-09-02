<template>
  <TransitionGroup name="toast" tag="div" class="toasts">
    <div v-for="t in toasts" :key="t.id" class="toast" :class="`toast--${t.type}`">
      <span class="toast__text">{{ t.text }}</span>
      <span v-if="t.count > 1" class="toast__count">{{ t.count }}</span>
      <button class="icon-btn toast__close" aria-label="Закрыть" @click="$emit('dismiss', t.id)">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="3.1" stroke-linecap="round">
          <path d="M18 6 6 18M6 6l12 12" />
        </svg>
      </button>
    </div>
  </TransitionGroup>
</template>

<script setup lang="ts">
import type { Toast } from '~/composables/useToasts'

// Показ уведомлений. Сами сообщения живут в useToasts: их шлют и страница, и
// то, что она вызывает, поэтому очередь принадлежит странице, а не этой
// карточке.
defineProps<{ toasts: Toast[] }>()
defineEmits<{ dismiss: [id: number] }>()
</script>
