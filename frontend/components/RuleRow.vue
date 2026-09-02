<template>
  <li
    :id="`rule-${rule.id}`"
    class="row"
    :class="{ 'row--match': matched, 'row--pending row--removing': removing }"
  >
    <span class="tag" :class="`tag--${rule.type}`">{{ ruleTypeLabel(rule.type) }}</span>
    <span class="row__value">{{ rule.value }}</span>
    <span v-if="removing" class="row__note">удаляем…</span>
    <span v-else-if="matched" class="row__note">уже есть</span>
    <button
      class="icon-btn icon-btn--danger"
      aria-label="Удалить"
      :disabled="removing"
      @click="$emit('delete', rule.id)"
    >
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
// removing — запрос на удаление уже отправлен. Служба в этот момент
// пересобирает маршруты на живом туннеле, и это занимает заметное время:
// без отметки строка стоит как ни в чём не бывало, и крестик жмут повторно.
defineProps<{ rule: RoutingRule; matched?: boolean; removing?: boolean }>()
defineEmits<{ delete: [id: string] }>()
</script>
