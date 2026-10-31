<template>
  <div>
    <section v-for="group in groups" :key="group.key" class="group" :class="`group--${group.key}`">
      <h3 class="group__head">
        <span class="group__mark" aria-hidden="true"></span>
        <span class="group__title">{{ group.title }}</span>
        <span class="group__count mono">{{ group.rules.length }}</span>
        <span class="group__note">{{ group.note }}</span>
      </h3>
      <ul class="rules">
        <li
          v-for="rule in group.rules"
          :key="rule.id"
          class="rule"
          :class="{ 'rule--dead': isDead(rule) }"
          :title="titleOf(rule)"
        >
          <span class="rule__text mono">{{ ruleText(rule) }}</span>
          <span v-if="countOf(rule)" class="rule__count mono">{{ countOf(rule) }}</span>
          <button class="rule__drop" :aria-label="`Убрать ${ruleText(rule)}`" title="Убрать" @click="remove(rule)">×</button>
        </li>
      </ul>
    </section>

    <p v-if="routing && !rules.length" class="empty">
      Список пуст — {{ mode === 'vpn_list' ? 'через VPN сейчас не идёт ничего' : 'через VPN сейчас идёт всё' }}.
      Начните, например, с <span class="mono">geosite:youtube</span> или <span class="mono">geoip:ru</span>.
    </p>
    <p v-else-if="filter && !groups.length" class="empty">Нет совпадений — Enter добавит набранное.</p>
  </div>
</template>

<script setup lang="ts">
// Список правил по группам. Убрать правило — ×.
import { computed } from 'vue'
import type { RoutingRule } from '../../api/types'
import { RULE_GROUPS, describeRule, isGeo, ruleText, sortKey } from '../../lib/rules'
import { useGeo } from '../../stores/geo'
import { useRouting } from '../../stores/routing'

const props = defineProps<{ filter: string }>()

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
.group {
  margin-bottom: 18px;
  --g: var(--g-name);
}

.group--app { --g: var(--g-app); }
.group--set { --g: var(--g-set); }
.group--addr { --g: var(--g-addr); }

.group__head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 8px;
  font-size: 13px;
  font-weight: normal;
}

.group__mark {
  width: 8px;
  height: 8px;
  align-self: center;
  border-radius: 2px;
  background: var(--g);
}

.group__title {
  font-weight: 600;
}

.group__count {
  color: var(--muted);
  font-size: 12px;
}

.group__note {
  color: var(--faint);
  font-size: 12px;
}

.rules {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.rule {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  height: 26px;
  padding-left: 8px;
  border: 1px solid var(--line);
  border-left: 2px solid var(--g);
  border-radius: 3px;
  background: var(--raise);
}

.rule:hover {
  border-color: var(--line-strong);
  border-left-color: var(--g);
}

.rule__text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rule__count {
  color: var(--faint);
  font-size: 11px;
}

.rule--dead .rule__text {
  color: var(--faint);
  text-decoration: line-through;
}

.rule__drop {
  width: 24px;
  height: 24px;
  color: var(--faint);
  font-size: 15px;
}

.rule__drop:hover {
  color: var(--bad);
}
</style>
