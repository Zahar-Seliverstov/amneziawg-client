// Поле ввода правил: разбор набранного, подсказки категорий и добавление.
//
// Одно поле на всё: набрал — Enter — правило в силе. Слово без точки ищет
// категорию в базах, текст в поле заодно фильтрует список, а вставленный
// список добавляется целиком.

import { computed, ref, watch, type Ref } from 'vue'
import type { GeoKind, RuleType } from '../api/types'
import { describeRule, geoProblem, isGeo, parseRule, ruleKey, ruleText, splitTokens } from '../lib/rules'
import { useGeo } from '../stores/geo'
import { useRouting } from '../stores/routing'

export interface Suggestion {
  text: string
  meta: string
}

const MAX_SUGGESTIONS = 8

export function useRuleInput(field: Ref<HTMLInputElement | null>) {
  const { mode, savedKeys, saving, apply } = useRouting()
  const { geo, counts } = useGeo()

  const query = ref('')
  const focused = ref(false)
  const cursor = ref(-1)
  const dismissed = ref(false)

  // Одно слово в поле — фильтр по списку правил.
  const filter = computed(() => {
    const tokens = splitTokens(query.value)
    return tokens.length === 1 ? tokens[0]!.toLowerCase() : ''
  })

  // Что получится из набранного: что добавится, сколько повторов и что не так.
  const plan = computed(() => {
    const add: string[] = []
    const seen = new Set<string>()
    let dup = 0
    let problem = ''
    let only: { type: RuleType; value: string } | null = null

    for (const token of splitTokens(query.value)) {
      const draft = parseRule(token)
      if (draft.kind === 'query') {
        problem ||= `«${token}» — не правило: домен пишется с точкой, зона — .${token}, набор — geosite:${token}`
        continue
      }
      if (draft.kind !== 'ready') {
        problem ||= draft.reason || `«${token}» не похоже на правило`
        continue
      }

      const rule = { type: draft.type!, value: draft.value! }
      const key = ruleKey(rule)
      if (savedKeys.value.has(key) || seen.has(key)) {
        dup++
        continue
      }

      const missing = geoProblem(geo.value, rule.type, rule.value)
      if (missing) {
        problem ||= missing
        continue
      }

      seen.add(key)
      add.push(ruleText(rule))
      only = rule
    }

    return { add, dup, problem, only: add.length === 1 ? only : null }
  })

  // ── Подсказки категорий geosite/geoip ───────────────────────────────
  const lastToken = computed(() => query.value.match(/[^\s,;]+$/)?.[0] || '')

  const suggestions = computed<Suggestion[]>(() => {
    const token = lastToken.value.toLowerCase()
    if (!token || !geo.value) return []

    let kinds: GeoKind[] = ['geosite', 'geoip']
    let needle = token
    const colon = token.indexOf(':')
    if (colon >= 0) {
      const kind = token.slice(0, colon)
      if (!isGeo(kind)) return []
      kinds = [kind]
      needle = token.slice(colon + 1)
    } else if (token.includes('.') || token.includes('/')) {
      return []
    }

    // Сначала категории, начинающиеся с набранного, потом содержащие его.
    const starts: Suggestion[] = []
    const contains: Suggestion[] = []
    for (const kind of kinds) {
      for (const tag of geo.value[kind].tags) {
        const text = `${kind}:${tag.name}`
        if (savedKeys.value.has(text)) continue
        const at = tag.name.indexOf(needle)
        if (at < 0) continue
        const item = { text, meta: `${tag.count.toLocaleString('ru-RU')} ${kind === 'geoip' ? 'подсетей' : 'записей'}` }
        ;(at === 0 ? starts : contains).push(item)
      }
    }

    const exact = (s: Suggestion) => (s.text.endsWith(`:${needle}`) ? 1 : 0)
    starts.sort((a, b) => exact(b) - exact(a) || a.text.length - b.text.length)
    return [...starts, ...contains].slice(0, MAX_SUGGESTIONS)
  })

  const suggestOpen = computed(() => focused.value && !dismissed.value && suggestions.value.length > 0)

  // Новое слово — новые подсказки. Курсор сразу на первой, только если
  // слово само правилом не является: иначе Enter добавил бы не то.
  watch(lastToken, token => {
    dismissed.value = false
    cursor.value = token && parseRule(token).kind !== 'ready' ? 0 : -1
  })

  const hint = computed(() => {
    if (!query.value.trim()) {
      return focused.value ? 'Enter — добавить · можно вставить список · слово без точки ищет категорию' : ''
    }

    const { add, dup, problem, only } = plan.value
    if (!add.length && suggestOpen.value && cursor.value >= 0) return '↑↓ — выбрать категорию, Enter — добавить, Tab — дописать'
    if (problem && !add.length) return problem

    const parts: string[] = []
    if (only) parts.push(describeRule(only, mode.value, counts.value))
    else if (add.length) parts.push(`Добавится правил: ${add.length}`)
    if (dup) parts.push(add.length || problem ? `уже в списке: ${dup}` : 'Уже в списке')
    if (problem && add.length) parts.push(problem)
    return parts.join(' · ')
  })

  // ── Действия ────────────────────────────────────────────────────────
  async function addAll() {
    const { add } = plan.value
    if (!add.length || saving.value) return
    if (!(await apply(add))) return

    // В поле остаётся только то, что не взяли: исправить и повторить.
    query.value = splitTokens(query.value)
      .filter(token => {
        const d = parseRule(token)
        return d.kind !== 'ready' || !savedKeys.value.has(ruleKey({ type: d.type!, value: d.value! }))
      })
      .join(' ')
  }

  function complete(s: Suggestion) {
    const head = query.value.slice(0, query.value.length - lastToken.value.length)
    query.value = head + s.text
    return head
  }

  // Выбор подсказки: единственное слово в поле — сразу добавляем.
  function pick(s: Suggestion) {
    const head = complete(s)
    dismissed.value = true
    if (!splitTokens(head).length) addAll()
  }

  function onKey(e: KeyboardEvent) {
    if (suggestOpen.value) {
      const n = suggestions.value.length
      const current = suggestions.value[cursor.value]
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault()
        const step = e.key === 'ArrowDown' ? 1 : -1
        cursor.value = (cursor.value + step + n) % n
        return
      }
      if (e.key === 'Tab' && current) {
        e.preventDefault()
        complete(current)
        return
      }
      if (e.key === 'Enter' && current) {
        e.preventDefault()
        pick(current)
        return
      }
    }

    if (e.key === 'Enter') {
      e.preventDefault()
      addAll()
    } else if (e.key === 'Escape') {
      if (suggestOpen.value) dismissed.value = true
      else query.value = ''
    }
  }

  // Вставленный многострочный список ложится в поле одной строкой:
  // поле однострочное и переводы строк иначе съело бы.
  function onPaste(e: ClipboardEvent) {
    const text = e.clipboardData?.getData('text') || ''
    if (!/[\r\n]/.test(text)) return

    e.preventDefault()
    const input = field.value
    const from = input?.selectionStart ?? query.value.length
    const to = input?.selectionEnd ?? from
    query.value = query.value.slice(0, from) + splitTokens(text).join(' ') + query.value.slice(to)
  }

  return {
    query, focused, cursor, filter, plan, hint, suggestions, suggestOpen, saving,
    addAll, pick, onKey, onPaste
  }
}
