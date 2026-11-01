<template>
  <div class="groups">
    <section v-for="group in groups" :key="group.key" class="group">
      <h3>
        <button
          type="button"
          class="group__head"
          :aria-expanded="isOpen(group.key)"
          :aria-controls="`rules-${group.key}`"
          :disabled="Boolean(filter)"
          :title="group.note"
          @click="toggle(group.key)"
        >
          <ChevronRight class="group__chev" aria-hidden="true" />
          <span>{{ group.title }}</span>
          <span class="group__count num">{{ group.rules.length }}</span>
        </button>
      </h3>
      <TransitionGroup v-show="isOpen(group.key)" :id="`rules-${group.key}`" tag="ul" name="chip" class="chips">
        <li
          v-for="rule in group.rules"
          :key="rule.id"
          class="chip"
          :class="{ 'chip--dead': isDead(rule) }"
          :title="titleOf(rule)"
        >
          <span class="chip__text">{{ ruleText(rule) }}</span>
          <span v-if="countOf(rule)" class="chip__count num">{{ countOf(rule) }}</span>
          <button type="button" class="chip__drop" :aria-label="`Убрать ${ruleText(rule)}`" title="Убрать" @click="remove(rule)">
            <X aria-hidden="true" />
          </button>
        </li>
      </TransitionGroup>
    </section>

    <p v-if="routing && !rules.length" class="empty">
      Список пуст — {{ mode === 'vpn_list' ? 'через VPN сейчас не идёт ничего' : 'через VPN сейчас идёт всё' }}.
      Начните, например, с geosite:youtube или geoip:ru.
    </p>
    <p v-else-if="filter && !groups.length" class="empty">Нет совпадений — Enter добавит набранное.</p>
  </div>
</template>

<script setup lang="ts">
// Список правил по группам. Группу можно свернуть — при полусотне правил
// сплошная стена плашек не читается; что свёрнуто, помнит браузер.
// Пока в поле набран фильтр, раскрыто всё: искомое не должно прятаться.
import { ChevronRight, X } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import type { RoutingRule } from '../../api/types'
import { RULE_GROUPS, describeRule, isGeo, ruleText, sortKey, type RuleGroupKey } from '../../lib/rules'
import { useGeo } from '../../stores/geo'
import { useRouting } from '../../stores/routing'

const props = defineProps<{ filter: string }>()

const COLLAPSED_KEY = 'awg-collapsed-groups'

const { routing, mode, rules, remove } = useRouting()
const { geo, counts } = useGeo()

const groups = computed(() =>
  RULE_GROUPS.map(group => ({
    ...group,
    rules: rules.value
      .filter(rule => group.types.includes(rule.type))
      .filter(rule => !props.filter || ruleText(rule).toLowerCase().includes(props.filter))
      .sort((a, b) => sortKey(a).localeCompare(sortKey(b)))
  })).filter(group => group.rules.length)
)

// ── Свёрнутые группы ────────────────────────────────────────────────
function readCollapsed(): RuleGroupKey[] {
  try {
    return JSON.parse(localStorage.getItem(COLLAPSED_KEY) || '[]')
  } catch {
    return []
  }
}

const collapsed = ref(new Set<RuleGroupKey>(readCollapsed()))

watch(collapsed, set => {
  try {
    localStorage.setItem(COLLAPSED_KEY, JSON.stringify([...set]))
  } catch {}
}, { deep: true })

function isOpen(key: RuleGroupKey) {
  return Boolean(props.filter) || !collapsed.value.has(key)
}

function toggle(key: RuleGroupKey) {
  if (collapsed.value.has(key)) collapsed.value.delete(key)
  else collapsed.value.add(key)
}

// ── Плашки ──────────────────────────────────────────────────────────
function countOf(rule: RoutingRule) {
  if (!isGeo(rule.type) || rule.value.includes('@')) return ''
  const n = counts.value[`${rule.type}:${rule.value}`]
  return n ? n.toLocaleString('ru-RU') : ''
}

// Правило набора, категории которого нет в базе, ничего не делает.
function isDead(rule: RoutingRule) {
  return isGeo(rule.type) && Boolean(geo.value) && !(`${rule.type}:${rule.value.split('@')[0]}` in counts.value)
}

function titleOf(rule: RoutingRule) {
  if (isDead(rule)) return 'Категории нет в базе — правило не действует'
  return describeRule(rule, mode.value, counts.value)
}
</script>

<style scoped>
.groups {
  display: grid;
  gap: 12px;
}

h3 {
  font: inherit;
}

/* Заголовок группы тихий — вторичным цветом, счётчик ещё тише: читаются
   сами правила, а не рамки вокруг них. */
.group__head {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  margin-left: -6px;
  padding: 0 8px 0 4px;
  border-radius: 6px;
  color: var(--muted);
  font-size: 13px;
  font-weight: 500;
  transition: color var(--t), background-color var(--t);
}

.group__head:hover:not(:disabled) {
  color: var(--text);
}

.group__head:disabled {
  cursor: default;
}

.group__chev {
  width: 14px;
  height: 14px;
  color: var(--faint);
}

.group__head[aria-expanded='true'] .group__chev {
  transform: rotate(90deg);
}

.group__count {
  color: var(--faint);
  font-weight: 400;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding-top: 6px;
}

/* Плашка — только фон, без рамки и цветных меток. Крестик лежит поверх
   конца плашки и виден при наведении и фокусе: пятьдесят крестиков сразу —
   это шум, а не информация. */
.chip {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  height: 28px;
  padding: 0 10px;
  border-radius: var(--r-chip);
  background: var(--surface);
  font-size: 13px;
  transition: background-color var(--t);
}

.chip:hover,
.chip:focus-within {
  background: var(--surface-hover);
}

.chip__text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chip__count {
  color: var(--faint);
  font-size: 12px;
}

.chip--dead .chip__text {
  color: var(--faint);
  text-decoration: line-through;
}

.chip__drop {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  display: grid;
  place-items: center;
  width: 26px;
  border-radius: 0 var(--r-chip) var(--r-chip) 0;
  background: var(--surface-hover);
  color: var(--muted);
  opacity: 0;
  transition: opacity var(--t), color var(--t);
}

.chip__drop svg {
  width: 14px;
  height: 14px;
}

.chip:hover .chip__drop,
.chip__drop:focus-visible {
  opacity: 1;
}

.chip__drop:hover {
  color: var(--text);
}

.chip__drop:focus-visible {
  outline-offset: -2px;
}

/* Без мыши наводить нечем — крестик виден всегда, но тихим цветом и
   своим местом, не поверх текста. */
@media (hover: none) {
  .chip {
    padding-right: 0;
  }

  .chip__drop {
    position: static;
    height: 28px;
    background: transparent;
    color: var(--faint);
    opacity: 1;
  }
}

/* Плашка исчезает коротким угасанием. */
.chip-enter-active,
.chip-leave-active {
  transition: opacity var(--t);
}

.chip-enter-from,
.chip-leave-to {
  opacity: 0;
}
</style>
