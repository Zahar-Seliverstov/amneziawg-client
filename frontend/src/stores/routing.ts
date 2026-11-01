// Правила маршрутизации: состояние и действия над ним.
//
// Компоненты только показывают и вызывают действия; как правила уходят в
// службу и что делать с отказом — решается здесь, в одном месте.

import { computed, ref, shallowRef } from 'vue'
import { api, errorText } from '../api/client'
import type { RejectedRule, RoutingBatchResult, RoutingConfig, RoutingMode, RoutingRule } from '../api/types'
import { ruleKey, ruleText } from '../lib/rules'
import { notify, notifyError } from './toasts'

const routing = ref<RoutingConfig | null>(null)
const switching = ref(false)
const saving = ref(false)

// Правила, удаление которых ушло в службу, но ответа ещё нет. С экрана они
// пропадают сразу, а из routing — только после ответа: так перечитанный
// посреди удаления список (load после пакетной правки) не вернёт их на
// экран, а отказ службы вернёт правило на место без повторного запроса.
const removing = shallowRef(new Set<string>())

const mode = computed(() => routing.value?.mode)
const rules = computed(() => (routing.value?.rules ?? []).filter(r => !removing.value.has(r.id)))

// Служба что-то применяет — показываем «применяется…».
const pending = computed(() => saving.value || switching.value || removing.value.size > 0)

// Ключи уже добавленных правил: по ним отсеиваются повторы при вводе.
const savedKeys = computed(() => new Set(rules.value.map(ruleKey)))

async function load() {
  try {
    routing.value = await api.getRouting()
  } catch (e) {
    notifyError(e)
  }
}

// Режим переключается на экране сразу; служба отказала — возвращаем.
async function setMode(next: RoutingMode) {
  const current = routing.value
  if (!current || switching.value || next === current.mode) return
  routing.value = { ...current, mode: next }
  switching.value = true
  try {
    await api.setRoutingMode(next)
  } catch (e) {
    if (routing.value?.mode === next) routing.value = { ...routing.value, mode: current.mode }
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

function markRemoving(id: string, on: boolean) {
  const next = new Set(removing.value)
  if (on) next.add(id)
  else next.delete(id)
  removing.value = next
}

// Удаление одного правила — сразу с экрана, не дожидаясь службы: крестик
// должен срабатывать мгновенно. Не вышло — правило возвращается на место.
async function remove(rule: RoutingRule) {
  if (!routing.value || removing.value.has(rule.id)) return
  markRemoving(rule.id, true)
  try {
    await api.batchRules([], [rule.id])
    if (routing.value) {
      routing.value = { ...routing.value, rules: routing.value.rules.filter(r => r.id !== rule.id) }
    }
  } catch (e) {
    notify(`Не удалось убрать ${ruleText(rule)}: ${errorText(e)}`)
  } finally {
    markRemoving(rule.id, false)
  }
}

export function useRouting() {
  return { routing, mode, rules, savedKeys, switching, saving, pending, load, setMode, apply, remove }
}
