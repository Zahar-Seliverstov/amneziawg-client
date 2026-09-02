<template>
  <li class="row" :class="{ 'row--match': matched }">
    <span class="tag" :class="`tag--${rule.type}`">{{ ruleTypeLabel(rule.type) }}</span>
    <span class="row__value">{{ rule.value }}</span>
    <span v-if="matched" class="row__note">уже есть</span>
    <button class="icon-btn icon-btn--danger" aria-label="Удалить" @click="$emit('delete', rule.id)">
      <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.9" stroke-linecap="round">
        <path d="M18 6 6 18M6 6l12 12" />
      </svg>
    </button>
  </li>
</template>

<script setup lang="ts">
import type { RoutingRule } from '~/composables/useApi'

// Одно правило в списке. Отдельным компонентом, потому что строка рисуется в
// двух местах: внутри группы и среди одиночных правил.
//
// matched — то самое правило, которое пользователь как раз набирает в поле.
// Отметка нужна, чтобы «такое правило уже есть» не осталось словами: видно,
// какое именно и где оно лежит.
defineProps<{ rule: RoutingRule; matched?: boolean }>()
defineEmits<{ delete: [id: string] }>()
</script>
