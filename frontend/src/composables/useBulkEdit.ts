// Правка правил текстом: весь список — по правилу на строку.
//
// Для чистки и больших вставок это быстрее, чем по одному: стёртая строка —
// удалённое правило, новая — добавленное. Сохраняется одним пакетом.

import { computed, ref } from 'vue'
import { parseRule, ruleKey, ruleText, splitTokens } from '../lib/rules'
import { useRouting } from '../stores/routing'
import { notify } from '../stores/toasts'

export function useBulkEdit() {
  const { rules, savedKeys, saving, apply } = useRouting()

  const open = ref(false)
  const draft = ref('')

  function start(extra: string[] = []) {
    draft.value = [...rules.value.map(ruleText), ...extra].join('\n') + '\n'
    open.value = true
  }

  function cancel() {
    open.value = false
  }

  const plan = computed(() => {
    const keys = new Map<string, string>()
    const bad: string[] = []

    for (const token of splitTokens(draft.value)) {
      const d = parseRule(token)
      if (d.kind !== 'ready') {
        bad.push(token)
        continue
      }
      const rule = { type: d.type!, value: d.value! }
      keys.set(ruleKey(rule), ruleText(rule))
    }

    const add = [...keys].filter(([key]) => !savedKeys.value.has(key)).map(([, text]) => text)
    const remove = rules.value.filter(r => !keys.has(ruleKey(r))).map(r => r.id)
    return { add, remove, bad }
  })

  const dirty = computed(() => plan.value.add.length > 0 || plan.value.remove.length > 0)

  // «+3 −1» — что изменится при сохранении.
  const change = computed(() => {
    const { add, remove } = plan.value
    return [add.length && `+${add.length}`, remove.length && `−${remove.length}`].filter(Boolean).join(' ')
  })

  const hint = computed(() => {
    const { bad } = plan.value
    if (!bad.length) return 'По правилу на строку, # — комментарий. Стёртая строка — удалённое правило.'
    const tail = bad.length > 3 ? ` и ещё ${bad.length - 3}` : ''
    return `Не похоже на правило: ${bad.slice(0, 3).join(', ')}${tail} — эти строки не сохранятся`
  })

  async function save() {
    const { add, remove } = plan.value
    const result = await apply(add, remove)
    if (!result) return

    open.value = false
    if (!result.rejected.length) {
      const parts = [
        result.added.length && `добавлено ${result.added.length}`,
        result.removed && `удалено ${result.removed}`
      ].filter(Boolean)
      notify(parts.length ? `Правила: ${parts.join(', ')}` : 'Изменений нет', 'ok')
    }
  }

  return { open, draft, plan, dirty, change, hint, saving, start, cancel, save }
}
