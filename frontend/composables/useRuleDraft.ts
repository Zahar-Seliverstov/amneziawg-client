// Разбор того, что пользователь набрал в поле правил.
//
// Поле одно на поиск и на добавление, поэтому вид правила не выбирается
// руками, а виден по самому значению: 1.1.1.1 — адрес, 10.0.0.0/8 — подсеть,
// .ru — зона, git.dtel.ru — домен. Ошибиться видом невозможно, а мусор не
// проходит вовсе.
//
// Проверка повторяет ту, что стоит в службе (internal/config/rules.go), и
// повторяет намеренно: служба обязана проверять всё, что к ней приходит, а
// окно обязано отвечать на набор текста мгновенно и не спрашивая никого.
// Разойтись они могут только в сторону строгости службы — она и решает.

export type RuleType = 'ip' | 'cidr' | 'domain' | 'zone'

export interface RuleDraft {
  // empty — поле пустое.
  // query — набранное годится как строка поиска, но правилом быть не пыталось
  //         (одно слово без точек: «dtel»).
  // invalid — правилом быть пыталось и не вышло: 10.0.0.0/44, 1.2.3.999.
  // ready — годное правило.
  kind: 'empty' | 'query' | 'invalid' | 'ready'
  type?: RuleType
  // value — приведённое к каноническому виду: без пробелов, имена в нижнем
  // регистре и без завершающей точки. Именно оно уходит в службу и по нему
  // же ищется совпадение с уже добавленным.
  value?: string
  reason?: string
}

const TYPE_LABELS: Record<RuleType, string> = {
  ip: 'IP',
  cidr: 'Подсеть',
  domain: 'Домен',
  zone: 'Зона'
}

export function ruleTypeLabel(type: string) {
  return TYPE_LABELS[type as RuleType] || type
}

export function parseRule(input: string): RuleDraft {
  const raw = input.trim()
  if (!raw) return { kind: 'empty' }

  if (raw.includes('/')) return parseCidr(raw)
  if (raw.startsWith('.')) return parseZone(raw)
  if (isIP(raw)) return { kind: 'ready', type: 'ip', value: raw.toLowerCase() }

  return parseDomain(raw)
}

function parseCidr(raw: string): RuleDraft {
  const [addr, prefix, ...rest] = raw.split('/')

  if (rest.length || !addr || !prefix) {
    return { kind: 'invalid', reason: 'Подсеть пишется как 10.0.0.0/8' }
  }
  if (!isIP(addr)) {
    return { kind: 'invalid', reason: `${addr} — не адрес: подсеть пишется как 10.0.0.0/8` }
  }

  const bits = Number(prefix)
  const max = addr.includes(':') ? 128 : 32

  if (!/^\d+$/.test(prefix) || bits > max) {
    return { kind: 'invalid', reason: `Длина маски — число от 0 до ${max}` }
  }

  return { kind: 'ready', type: 'cidr', value: `${addr.toLowerCase()}/${bits}` }
}

function parseZone(raw: string): RuleDraft {
  const name = raw.slice(1).toLowerCase().replace(/\.$/, '')

  const bad = hostnameProblem(name)
  if (bad) return { kind: 'invalid', reason: `Зона пишется как .ru — ${bad}` }

  return { kind: 'ready', type: 'zone', value: `.${name}` }
}

function parseDomain(raw: string): RuleDraft {
  const name = raw.toLowerCase().replace(/\.$/, '')

  // Слово без точки — это поиск по списку, а не попытка добавить правило.
  // Домен верхнего уровня целиком добавляют зоной: «.ru», а не «ru».
  if (!name.includes('.')) return { kind: 'query' }

  const bad = hostnameProblem(name)
  if (bad) return { kind: 'invalid', reason: `Не похоже на доменное имя: ${bad}` }

  return { kind: 'ready', type: 'domain', value: name }
}

// hostnameProblem возвращает причину, по которой имя не годится, или пустую
// строку. Правила мягкие: цель — отсеять явный мусор вроде пробелов, схем и
// путей, а не проверить имя на соответствие RFC.
function hostnameProblem(name: string): string {
  if (!name) return 'пустое имя'
  if (name.length > 253) return 'слишком длинное имя'
  if (/[\s/\\:@]/.test(name)) return 'лишние символы'

  for (const label of name.split('.')) {
    if (!label) return 'пустая часть имени'
    if (label.length > 63) return 'слишком длинная часть имени'
    if (label.startsWith('-') || label.endsWith('-')) return 'часть имени начинается или кончается дефисом'
    // Не-ASCII пропускаем: имена на кириллице пользователь вправе вписать
    // как есть.
    if (/[\x00-\x2c\x2f\x3a-\x40\x5b-\x5e\x60\x7b-\x7f]/.test(label)) return 'недопустимый символ'
  }

  return ''
}

function isIP(value: string) {
  return isIPv4(value) || isIPv6(value)
}

function isIPv4(value: string) {
  const parts = value.split('.')
  if (parts.length !== 4) return false

  return parts.every(part => {
    if (!/^\d{1,3}$/.test(part)) return false
    // Ведущий ноль запрещён: 010.0.0.1 в разных системах читается по-разному,
    // и служба такой адрес тоже не примет.
    if (part.length > 1 && part.startsWith('0')) return false
    return Number(part) <= 255
  })
}

function isIPv6(value: string) {
  if (!value.includes(':')) return false

  const halves = value.split('::')
  if (halves.length > 2) return false

  const groups = halves.map(half => (half ? half.split(':') : []))
  const flat = groups.flat()

  // Последняя часть может быть записана как IPv4: ::ffff:192.168.0.1
  let count = flat.length
  const last = flat[flat.length - 1]
  if (last && last.includes('.')) {
    if (!isIPv4(last)) return false
    count += 1
  }

  for (const group of flat) {
    if (group.includes('.')) continue
    if (!/^[0-9a-fA-F]{1,4}$/.test(group)) return false
  }

  return halves.length === 2 ? count <= 7 : count === 8
}
