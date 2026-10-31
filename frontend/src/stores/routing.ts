// Правила маршрутизации: состояние и действия над ним.
//
// Компоненты только показывают и вызывают действия; как правила уходят в
// службу и что делать с отказом — решается здесь, в одном месте.

import { computed, ref } from 'vue'
import { api } from '../api/client'
import type { RejectedRule, RoutingBatchResult, RoutingConfig, RoutingMode, RoutingRule } from '../api/types'
import { ruleKey } from '../lib/rules'
import { notify, notifyError } from './toasts'

const routing = ref<RoutingConfig | null>(null)
const switching = ref(false)
const saving = ref(false)

const mode = computed(() => routing.value?.mode)
const rules = computed(() => routing.value?.rules ?? [])

// Ключи уже добавленных правил: по ним отсеиваются повторы при вводе.
const savedKeys = computed(() => new Set(rules.value.map(ruleKey)))

async function load() {
  try {
    routing.value = await api.getRouting()
  } catch (e) {
    notifyError(e)
  }
}

async function setMode(next: RoutingMode) {
  if (switching.value || next === mode.value) return
  switching.value = true
  try {
    await api.setRoutingMode(next)
    if (routing.value) routing.value = { ...routing.value, mode: next }
  } catch (e) {
    notifyError(e)
  } finally {
    switching.value = false
  }
}

function reportRejected(rejected: RejectedRule[]) {
  if (!rejected.length) return
  const first = rejected[0]!
  const tail = rejected.length > 1 ? ` (и ещё ${rejected.length - 1})` : ''
  notify(`Не взяли ${first.value}: ${first.reason}${tail}`)
}

// Пакетная правка. Возвращает результат или null, если запрос не прошёл
// (об ошибке уже сказано). Отвергнутые правила тоже уже показаны.
async function apply(add: string[], remove: string[] = []): Promise<RoutingBatchResult | null> {
  if (saving.value) return null
  saving.value = true
  try {
    const result = await api.batchRules(add, remove)
    await load()
    reportRejected(result.rejected)
    return result
  } catch (e) {
    notifyError(e)
    return null
  } finally {
    saving.value = false
  }
}

// Удаление одного правила — сразу с экрана, не дожидаясь службы: крестик
// должен срабатывать мгновенно. Не вышло — возвращаем список как был.
async function remove(rule: RoutingRule) {
  if (!routing.value) return
  routing.value = { ...routing.value, rules: routing.value.rules.filter(r => r.id !== rule.id) }
  try {
    await api.batchRules([], [rule.id])
  } catch (e) {
    notifyError(e)
    await load()
  }
}

export function useRouting() {
  return { routing, mode, rules, savedKeys, switching, saving, load, setMode, apply, remove }
}
