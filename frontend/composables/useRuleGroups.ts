import type { RoutingRule } from './useApi'

// Группировка правил по общему источнику.
//
// Список правил быстро превращается в кашу: git.dtel.ru, bitrix.dtel.ru и
// адрес того же сервера — на вид три разные записи, а на деле одно хозяйство.
// Источник для каждого правила считает служба (GET /api/routing/sources),
// здесь только раскладка по группам и цвет.
//
// Группа появляется от двух правил: одинокая запись под собственным
// заголовком — это тот же список, только вдвое длиннее.

// Сколько цветов в палитре (см. --group-N в main.css). Больше групп — цвета
// пойдут по кругу: соседние всё равно останутся разными, а различать десять
// оттенков глазом человек всё равно не станет.
export const GROUP_COLORS = 6

export interface RuleGroup {
  source: string
  color: number
  rules: RoutingRule[]
}

export function useRuleGroups(
  rules: MaybeRefOrGetter<RoutingRule[]>,
  sources: MaybeRefOrGetter<Record<string, string>>
) {
  // Группы считаем по всему списку, а не по отфильтрованному: иначе поиск
  // разваливал бы группы на глазах, и по одному найденному правилу нельзя
  // было бы понять, к чему оно относится.
  const groups = computed<RuleGroup[]>(() => {
    const all = toValue(rules)
    const bySource = toValue(sources)

    const found = new Map<string, RoutingRule[]>()

    for (const rule of all) {
      const source = bySource[rule.id]
      if (!source) continue

      const list = found.get(source)
      if (list) list.push(rule)
      else found.set(source, [rule])
    }

    // Порядок — по первому появлению в списке, а не по алфавиту: так цвет
    // группы не перескакивает на другую при добавлении правила.
    return [...found.entries()]
      .filter(([, list]) => list.length >= 2)
      .map(([source, list], index) => ({ source, rules: list, color: index % GROUP_COLORS + 1 }))
  })

  // Правила, которым не с кем встать в пару.
  const loose = computed(() => {
    const grouped = new Set(groups.value.flatMap(g => g.rules.map(r => r.id)))
    return toValue(rules).filter(rule => !grouped.has(rule.id))
  })

  return { groups, loose }
}
