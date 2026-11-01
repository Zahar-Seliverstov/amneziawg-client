<template>
  <div class="mode">
    <Segmented
      :options="MODES"
      :model-value="mode"
      label="Режим маршрутизации: что идёт через VPN"
      :disabled="!mode"
      @update:model-value="setMode"
    />
    <p class="mode__note">{{ current?.note }}</p>
  </div>
</template>

<script setup lang="ts">
// Режим маршрутизации: что идёт в туннель — список или всё, кроме него.
// Применяется сразу, без кнопки «сохранить»: на экране — мгновенно, служба
// догоняет (см. setMode в stores/routing).
import { computed } from 'vue'
import type { RoutingMode } from '../../api/types'
import { useRouting } from '../../stores/routing'
import Segmented from '../ui/Segmented.vue'

const MODES: { value: RoutingMode; title: string; note: string }[] = [
  { value: 'vpn_list', title: 'Только список', note: 'Правила ниже — через VPN. Остальное идёт напрямую, как без VPN.' },
  { value: 'direct_list', title: 'Всё, кроме списка', note: 'Всё идёт через VPN. Правила ниже — напрямую, мимо туннеля.' }
]

const { mode, setMode } = useRouting()
const current = computed(() => MODES.find(m => m.value === mode.value))
</script>

<style scoped>
.mode {
  display: grid;
  justify-items: start;
  gap: 8px;
  min-width: 0;
}

.mode__note {
  color: var(--muted);
  font-size: 13px;
}
</style>
