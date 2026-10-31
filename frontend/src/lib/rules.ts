// Разбор того, что пользователь набрал в поле правил.
//
// Вид правила не выбирается руками, а виден по самому значению: 1.1.1.1 —
// адрес, 10.0.0.0/8 — подсеть, .ru — зона, git.example.org — домен. Наборы и
// особые виды имён пишутся с приставкой, как в xray: geoip:ru,
// geosite:youtube, full:, keyword:, regexp:.
//
// Проверка повторяет ту, что стоит в службе (internal/config/rules.go), и
// повторяет намеренно: служба обязана проверять всё, что к ней приходит, а
// окно обязано отвечать на набор текста мгновенно и не спрашивая никого.
// Разойтись они могут только в сторону строгости службы — она и решает.

import type { GeoState, RoutingMode, RoutingRule, RuleType } from '../api/types'


export interface RuleDraft {
  // empty — поле пустое.
  // query — одно слово без точки: «youtube». Правилом само по себе не
  //         станет, но годится для поиска категории базы.
  // invalid — правилом быть пыталось и не вышло: 10.0.0.0/44, 1.2.3.999.
  // ready — годное правило.
  kind: 'empty' | 'query' | 'invalid' | 'ready'
  type?: RuleType
  // value — приведённое к каноническому виду, без приставки: именно оно
  // уходит в службу и по нему же ищется совпадение с уже добавленным.
  value?: string
  reason?: string
}

const TYPE_LABELS: Record<RuleType, string> = {
  ip: 'IP',
  cidr: 'Подсеть',
  domain: 'Домен',
  zone: 'Зона',
  full: 'Точное имя',
  keyword: 'Слово в имени',
  regexp: 'Выражение',
  geoip: 'Сети из базы',
  geosite: 'Сайты из базы',
  app: 'Программа'
}

export function ruleTypeLabel(type: string) {
  return TYPE_LABELS[type as RuleType] || type
}

// Виды, которые пишутся с приставкой, как в xray. Остальные узнаются по
// самому значению.
const PREFIXED: Record<string, RuleType> = {
  domain: 'domain',
  full: 'full',
  keyword: 'keyword',
  regexp: 'regexp',
  geoip: 'geoip',
  geosite: 'geosite',
  app: 'app'
}

const GEO_TAG = /^[a-z0-9][a-z0-9!_.-]*(@[a-z0-9!_-]+)*$/

// Имя программы — как в службе (APP_NAME там же, internal/config/rules.go):
// слово без пробелов и путей, его сравнивают с именем процесса.
const APP_NAME = /^[a-z0-9][a-z0-9._+-]{1,63}$/

// Хвост, берущий у программы только UDP: app:vesktop/udp (AppUDPSuffix в
// службе).
export const APP_UDP_SUFFIX = '/udp'

// splitApp разбирает значение правила app: на имя и признак «только UDP».
export function splitApp(value: string) {
  const udp = value.endsWith(APP_UDP_SUFFIX)
  return { name: udp ? value.slice(0, -APP_UDP_SUFFIX.length) : value, udp }
}

// ruleText — правило так, как его пишет человек: с приставкой там, где без
// неё вид не угадать. Обратное к parseRule.
export function ruleText(rule: Pick<RoutingRule, 'type' | 'value'>) {
  return rule.type in PREFIXED && rule.type !== 'domain' ? `${rule.type}:${rule.value}` : rule.value
}

// ruleKey — правило для сравнения наборов, как у службы: вид и значение без
// учёта регистра.
export function ruleKey(rule: Pick<RoutingRule, 'type' | 'value'>) {
  return `${rule.type}:${rule.value.trim().toLowerCase()}`
}

export function parseRule(input: string): RuleDraft {
  const raw = input.trim()
  if (!raw) return { kind: 'empty' }

  const colon = raw.indexOf(':')
  if (colon > 0) {
    const type = PREFIXED[raw.slice(0, colon).toLowerCase()]
    if (type) return parsePrefixed(type, raw.slice(colon + 1).trim())
  }

  if (raw.includes('/')) return parseCidr(raw)
  if (raw.startsWith('.')) return parseZone(raw)

  // Точка в конце — полная запись имени, «1.1.1.1.» всё равно адрес.
  const bare = raw.replace(/\.$/, '')
  if (isIP(bare)) return { kind: 'ready', type: 'ip', value: bare.toLowerCase() }

  return parseDomain(raw)
}

function parsePrefixed(type: RuleType, rest: string): RuleDraft {
  if (!rest) return { kind: 'invalid', reason: `После «${type}:» нужно значение` }

  switch (type) {
    case 'geoip':
    case 'geosite': {
      const tag = rest.toLowerCase()
      if (!GEO_TAG.test(tag) || (type === 'geoip' && tag.includes('@'))) {
        return { kind: 'invalid', reason: `Не похоже на категорию ${type}` }
      }
      return { kind: 'ready', type, value: tag }
    }
    case 'regexp':
      try {
        new RegExp(rest)
      } catch {
        return { kind: 'invalid', reason: 'Выражение не разобрано' }
      }
      return { kind: 'ready', type, value: rest }
    case 'app': {
      const value = rest.toLowerCase()
      if (!APP_NAME.test(splitApp(value).name)) {
        return { kind: 'invalid', reason: 'Имя программы — одно слово: minecraft, firefox, steam; только UDP — vesktop/udp' }
      }
      return { kind: 'ready', type, value }
    }
    case 'keyword':
      if (/[\s/\\:@]/.test(rest)) return { kind: 'invalid', reason: 'Слово не должно содержать пробелов и знаков / : @' }
      return { kind: 'ready', type, value: rest.toLowerCase() }
    default: {
      const name = rest.toLowerCase().replace(/\.$/, '')
      const bad = hostnameProblem(name)
      if (bad) return { kind: 'invalid', reason: `Не похоже на доменное имя: ${bad}` }
      return { kind: 'ready', type, value: name }
    }
  }
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

  const labels = name.split('.')
  // Домена верхнего уровня из одних цифр не бывает (RFC 3696), а вот
  // опечатка в адресе выглядит именно так: 1.2.3.999, 10.0.0.1.5.
  if (/^\d+$/.test(labels[labels.length - 1]!)) return 'похоже на адрес с ошибкой'

  for (const label of labels) {
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

// ── Группы правил на экране ────────────────────────────────────────────
// Правила показываются группами по смыслу: чем правило решается — программой,
// набором из базы, именем или адресом. У каждой группы свой цвет метки.

export type RuleGroupKey = 'app' | 'set' | 'name' | 'addr'

export interface RuleGroup {
  key: RuleGroupKey
  title: string
  note: string
  types: RuleType[]
}

export const RULE_GROUPS: RuleGroup[] = [
  { key: 'app', title: 'Программы', note: 'по процессу, все его соединения', types: ['app'] },
  { key: 'set', title: 'Наборы из баз', note: 'geosite — сайты, geoip — сети', types: ['geosite', 'geoip'] },
  { key: 'name', title: 'Сайты', note: 'домен вместе с поддоменами', types: ['domain', 'zone', 'full', 'keyword', 'regexp'] },
  { key: 'addr', title: 'Адреса', note: 'IP и подсети', types: ['ip', 'cidr'] }
]

export function groupOf(type: RuleType): RuleGroupKey {
  return RULE_GROUPS.find(g => g.types.includes(type))?.key ?? 'name'
}

// Порядок внутри группы: имена — по зоне справа налево, чтобы поддомены
// одного хозяйства стояли рядом (api.vk.ru рядом с vk.ru).
export function sortKey(rule: Pick<RoutingRule, 'type' | 'value'>) {
  if (rule.type === 'domain' || rule.type === 'zone' || rule.type === 'full') {
    return rule.value.replace(/^\./, '').split('.').reverse().join('.')
  }
  return ruleText(rule)
}

// ── Базы geoip/geosite ─────────────────────────────────────────────────

export function isGeo(type: RuleType | string): type is 'geoip' | 'geosite' {
  return type === 'geoip' || type === 'geosite'
}

// Число записей по каждой категории: «geosite:youtube» → 312.
export function geoTagCounts(geo: GeoState | null): Record<string, number> {
  const out: Record<string, number> = {}
  if (!geo) return out
  for (const kind of ['geoip', 'geosite'] as const) {
    for (const tag of geo[kind]?.tags || []) out[`${kind}:${tag.name}`] = tag.count
  }
  return out
}

// Почему правило набора не сработает: базы нет или категории в ней нет.
export function geoProblem(geo: GeoState | null, type: RuleType, value: string): string {
  if (!isGeo(type) || !geo) return ''
  const info = geo[type]
  if (!info.present) return `База ${type}.dat не загружена — скачайте её в настройках`
  const tag = value.split('@')[0]
  if (!info.tags.some(t => t.name === tag)) return `В ${type}.dat нет категории «${value}»`
  return ''
}

// Пояснение к правилу: что именно оно охватывает и куда направит.
export function describeRule(
  rule: Pick<RoutingRule, 'type' | 'value'>,
  mode: RoutingMode | undefined,
  counts: Record<string, number>
): string {
  const n = counts[`${rule.type}:${rule.value}`]
  const num = (x: number) => x.toLocaleString('ru-RU')
  switch (rule.type) {
    case 'domain': return 'Домен вместе со всеми поддоменами'
    case 'zone': return `Все имена, кончающиеся на ${rule.value}`
    case 'full': return 'Ровно это имя, без поддоменов'
    case 'keyword': return 'Все имена, где встречается это слово'
    case 'regexp': return 'Имена по регулярному выражению'
    case 'geosite': return n ? `Сайты из базы: ${num(n)} записей` : 'Сайты из базы'
    case 'geoip': return n ? `Сети из базы: ${num(n)} подсетей` : 'Сети из базы'
    case 'app': {
      const { name, udp } = splitApp(rule.value)
      const what = udp ? `UDP программы «${name}» (голос, видео, игры)` : `Все соединения программы «${name}»`
      return `${what} — ${mode === 'vpn_list' ? 'через VPN' : 'мимо туннеля'}`
    }
    case 'cidr': return 'Подсеть'
    default: return 'Адрес'
  }
}

// Список правил из свободного текста: разделители — пробелы, запятые,
// точки с запятой и переводы строк, # — комментарий до конца строки.
export function splitTokens(text: string): string[] {
  return text
    .split(/\r?\n/)
    .flatMap(line => line.replace(/#.*/, '').split(/[\s,;]+/))
    .filter(Boolean)
}
