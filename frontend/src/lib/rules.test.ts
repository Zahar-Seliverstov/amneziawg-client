import { describe, it, expect } from 'vitest'
import {
  parseRule,
  ruleText,
  ruleKey,
  splitApp,
  groupOf,
  sortKey,
  isGeo,
  geoTagCounts,
  geoProblem,
  describeRule,
  splitTokens,
  ruleTypeLabel,
  APP_UDP_SUFFIX
} from './rules'
import type { GeoInfo, GeoState, RuleType } from '../api/types'

const NBSP = '\u00A0'

function ready(type: RuleType, value: string) {
  return { kind: 'ready', type, value }
}

function geoInfo(over: Partial<GeoInfo> = {}): GeoInfo {
  return { present: true, url: 'https://example.test/list.dat', tags: [], ...over }
}

function geoState(over: Partial<Record<'geoip' | 'geosite', GeoInfo>> = {}): GeoState {
  return { geoip: geoInfo(), geosite: geoInfo(), ...over }
}

describe('parseRule: адреса', () => {
  it('узнаёт IPv4 и приводит его к нижнему регистру', () => {
    expect(parseRule('1.1.1.1')).toEqual(ready('ip', '1.1.1.1'))
    expect(parseRule('192.168.0.255')).toEqual(ready('ip', '192.168.0.255'))
    expect(parseRule('0.0.0.0')).toEqual(ready('ip', '0.0.0.0'))
  })

  it('узнаёт IPv6, включая сокращённую запись и IPv4 в хвосте', () => {
    expect(parseRule('2001:db8::1')).toEqual(ready('ip', '2001:db8::1'))
    expect(parseRule('::1')).toEqual(ready('ip', '::1'))
    expect(parseRule('::ffff:192.168.0.1')).toEqual(ready('ip', '::ffff:192.168.0.1'))
    expect(parseRule('2001:DB8::A')).toEqual(ready('ip', '2001:db8::a'))
  })

  it('опечатка в адресе — ошибка, а не домен', () => {
    for (const value of ['1.2.3.999', '01.2.3.4', '1.2.3.4.5']) {
      expect(parseRule(value).kind).toBe('invalid')
    }
    // Цифры в начале имени — это нормально.
    expect(parseRule('4pda.to')).toEqual(ready('domain', '4pda.to'))
  })

  it('адрес с концевой точкой — всё равно адрес', () => {
    expect(parseRule('1.1.1.1.')).toEqual(ready('ip', '1.1.1.1'))
  })
})

describe('parseRule: подсети', () => {
  it('принимает IPv4-подсети и нормализует маску', () => {
    expect(parseRule('10.0.0.0/8')).toEqual(ready('cidr', '10.0.0.0/8'))
    expect(parseRule('192.168.0.0/16')).toEqual(ready('cidr', '192.168.0.0/16'))
    expect(parseRule('0.0.0.0/0')).toEqual(ready('cidr', '0.0.0.0/0'))
    expect(parseRule('10.0.0.0/08')).toEqual(ready('cidr', '10.0.0.0/8'))
  })

  it('принимает IPv6-подсети', () => {
    expect(parseRule('2001:db8::/64')).toEqual(ready('cidr', '2001:db8::/64'))
    expect(parseRule('::/0')).toEqual(ready('cidr', '::/0'))
  })

  it('отвергает маску больше допустимой', () => {
    expect(parseRule('10.0.0.0/33')).toEqual({
      kind: 'invalid',
      reason: 'Длина маски — число от 0 до 32'
    })
    expect(parseRule('2001:db8::/129')).toEqual({
      kind: 'invalid',
      reason: 'Длина маски — число от 0 до 128'
    })
  })

  it('отвергает кривую запись подсети', () => {
    expect(parseRule('10.0.0.0/8/9')).toEqual({
      kind: 'invalid',
      reason: 'Подсеть пишется как 10.0.0.0/8'
    })
    expect(parseRule('10.0.0.0/')).toEqual({
      kind: 'invalid',
      reason: 'Подсеть пишется как 10.0.0.0/8'
    })
    expect(parseRule('/8')).toEqual({
      kind: 'invalid',
      reason: 'Подсеть пишется как 10.0.0.0/8'
    })
    expect(parseRule('10.0.0.999/8')).toEqual({
      kind: 'invalid',
      reason: '10.0.0.999 — не адрес: подсеть пишется как 10.0.0.0/8'
    })
    expect(parseRule('10.0.0.0/abc')).toEqual({
      kind: 'invalid',
      reason: 'Длина маски — число от 0 до 32'
    })
    expect(parseRule('10.0.0.0/-1')).toEqual({
      kind: 'invalid',
      reason: 'Длина маски — число от 0 до 32'
    })
  })
})

describe('parseRule: зоны и домены', () => {
  it('узнаёт зону по ведущей точке', () => {
    expect(parseRule('.ru')).toEqual(ready('zone', '.ru'))
    expect(parseRule('.RU')).toEqual(ready('zone', '.ru'))
    expect(parseRule('.ru.')).toEqual(ready('zone', '.ru'))
  })

  it('отвергает пустую и кривую зону', () => {
    expect(parseRule('.')).toEqual({ kind: 'invalid', reason: 'Зона пишется как .ru — пустое имя' })
    expect(parseRule('.bad zone')).toEqual({
      kind: 'invalid',
      reason: 'Зона пишется как .ru — лишние символы'
    })
  })

  it('узнаёт домен и нормализует регистр с концевой точкой', () => {
    expect(parseRule('git.example.org')).toEqual(ready('domain', 'git.example.org'))
    expect(parseRule('Example.COM')).toEqual(ready('domain', 'example.com'))
    expect(parseRule('example.com.')).toEqual(ready('domain', 'example.com'))
  })

  it('слово без точки — поисковый запрос, а не правило', () => {
    expect(parseRule('youtube')).toEqual({ kind: 'query' })
    expect(parseRule('localhost')).toEqual({ kind: 'query' })
    expect(parseRule('ru')).toEqual({ kind: 'query' })
  })

  it('отвергает мусор в имени', () => {
    expect(parseRule('example..com')).toEqual({
      kind: 'invalid',
      reason: 'Не похоже на доменное имя: пустая часть имени'
    })
    expect(parseRule('bad-.com')).toEqual({
      kind: 'invalid',
      reason: 'Не похоже на доменное имя: часть имени начинается или кончается дефисом'
    })
    expect(parseRule('exa mple.com')).toEqual({
      kind: 'invalid',
      reason: 'Не похоже на доменное имя: лишние символы'
    })
  })
})

describe('parseRule: приставки', () => {
  it('распознаёт приставку без учёта регистра', () => {
    expect(parseRule('GEOSITE:YouTube')).toEqual(ready('geosite', 'youtube'))
    expect(parseRule('GeoIP:RU')).toEqual(ready('geoip', 'ru'))
  })

  it('разбирает geosite и geoip, включая атрибут @', () => {
    expect(parseRule('geosite:youtube')).toEqual(ready('geosite', 'youtube'))
    expect(parseRule('geosite:youtube@cn')).toEqual(ready('geosite', 'youtube@cn'))
    expect(parseRule('geoip:ru')).toEqual(ready('geoip', 'ru'))
    expect(parseRule('geoip:ru@cn')).toEqual({
      kind: 'invalid',
      reason: 'Не похоже на категорию geoip'
    })
  })

  it('разбирает full, keyword и regexp', () => {
    expect(parseRule('full:Example.COM')).toEqual(ready('full', 'example.com'))
    expect(parseRule('full:example.com.')).toEqual(ready('full', 'example.com'))
    expect(parseRule('keyword:Google')).toEqual(ready('keyword', 'google'))
    expect(parseRule('keyword:a b')).toEqual({
      kind: 'invalid',
      reason: 'Слово не должно содержать пробелов и знаков / : @'
    })
    expect(parseRule('keyword:a/b')).toEqual({
      kind: 'invalid',
      reason: 'Слово не должно содержать пробелов и знаков / : @'
    })
    expect(parseRule('regexp:^Ads\\.')).toEqual(ready('regexp', '^Ads\\.'))
    expect(parseRule('regexp:(')).toEqual({
      kind: 'invalid',
      reason: 'Выражение не разобрано'
    })
  })

  it('разбирает app и хвост /udp', () => {
    expect(parseRule('app:Firefox')).toEqual(ready('app', 'firefox'))
    expect(parseRule('app:vesktop/udp')).toEqual(ready('app', 'vesktop/udp'))
    expect(parseRule('app:Vesktop/UDP')).toEqual(ready('app', 'vesktop/udp'))
    expect(parseRule('app:two words')).toEqual({
      kind: 'invalid',
      reason: 'Имя программы — одно слово: minecraft, firefox, steam; только UDP — vesktop/udp'
    })
  })

  it('разбирает domain с приставкой', () => {
    expect(parseRule('domain:Example.com')).toEqual(ready('domain', 'example.com'))
  })

  it('требует значение после приставки', () => {
    expect(parseRule('geoip:')).toEqual({
      kind: 'invalid',
      reason: 'После «geoip:» нужно значение'
    })
    expect(parseRule('app:')).toEqual({
      kind: 'invalid',
      reason: 'После «app:» нужно значение'
    })
  })

  it('пустая строка — empty', () => {
    expect(parseRule('')).toEqual({ kind: 'empty' })
    expect(parseRule('   \t ')).toEqual({ kind: 'empty' })
  })
})

describe('ruleText и ruleKey', () => {
  it('не пишет приставку у domain и узнаваемых по значению видов', () => {
    expect(ruleText({ type: 'domain', value: 'example.com' })).toBe('example.com')
    expect(ruleText({ type: 'zone', value: '.ru' })).toBe('.ru')
    expect(ruleText({ type: 'ip', value: '1.1.1.1' })).toBe('1.1.1.1')
    expect(ruleText({ type: 'cidr', value: '10.0.0.0/8' })).toBe('10.0.0.0/8')
  })

  it('пишет приставку у наборов, точного имени, слова, regexp и app', () => {
    expect(ruleText({ type: 'geosite', value: 'youtube' })).toBe('geosite:youtube')
    expect(ruleText({ type: 'geoip', value: 'ru' })).toBe('geoip:ru')
    expect(ruleText({ type: 'full', value: 'example.com' })).toBe('full:example.com')
    expect(ruleText({ type: 'keyword', value: 'ads' })).toBe('keyword:ads')
    expect(ruleText({ type: 'regexp', value: '^ads\\.' })).toBe('regexp:^ads\\.')
    expect(ruleText({ type: 'app', value: 'firefox' })).toBe('app:firefox')
  })

  it('сравнивает без учёта регистра и с обрезкой пробелов', () => {
    expect(ruleKey({ type: 'domain', value: ' Example.COM ' })).toBe('domain:example.com')
    expect(ruleKey({ type: 'geosite', value: 'YouTube' })).toBe('geosite:youtube')
    expect(ruleKey({ type: 'domain', value: 'example.com' })).not.toBe(
      ruleKey({ type: 'full', value: 'example.com' })
    )
  })
})

describe('splitApp', () => {
  it('отделяет хвост /udp', () => {
    expect(APP_UDP_SUFFIX).toBe('/udp')
    expect(splitApp('firefox')).toEqual({ name: 'firefox', udp: false })
    expect(splitApp('vesktop/udp')).toEqual({ name: 'vesktop', udp: true })
  })

  it('хвост чувствителен к регистру, пустое имя не ломает разбор', () => {
    expect(splitApp('vesktop/UDP')).toEqual({ name: 'vesktop/UDP', udp: false })
    expect(splitApp('')).toEqual({ name: '', udp: false })
    expect(splitApp('/udp')).toEqual({ name: '', udp: true })
  })
})

describe('groupOf', () => {
  it('раскладывает все виды правил по группам', () => {
    expect(groupOf('app')).toBe('app')
    expect(groupOf('geosite')).toBe('set')
    expect(groupOf('geoip')).toBe('set')
    for (const type of ['domain', 'zone', 'full', 'keyword', 'regexp'] as const) {
      expect(groupOf(type)).toBe('name')
    }
    for (const type of ['ip', 'cidr'] as const) {
      expect(groupOf(type)).toBe('addr')
    }
  })
})

describe('sortKey', () => {
  it('переворачивает домен, чтобы поддомены шли рядом', () => {
    expect(sortKey({ type: 'domain', value: 'api.vk.ru' })).toBe('ru.vk.api')
    expect(sortKey({ type: 'domain', value: 'vk.ru' })).toBe('ru.vk')
    expect(sortKey({ type: 'zone', value: '.ru' })).toBe('ru')
    expect(sortKey({ type: 'full', value: 'www.example.com' })).toBe('com.example.www')
  })

  it('ставит api.vk.ru и vk.ru соседними при сортировке', () => {
    const rules = [
      { type: 'domain' as const, value: 'api.vk.ru' },
      { type: 'domain' as const, value: 'mail.ru' },
      { type: 'domain' as const, value: 'vk.ru' }
    ]
    const sorted = [...rules].sort((a, b) => sortKey(a).localeCompare(sortKey(b)))
    expect(sorted.map(r => r.value)).toEqual(['mail.ru', 'vk.ru', 'api.vk.ru'])
    expect(Math.abs(sorted.findIndex(r => r.value === 'vk.ru') - sorted.findIndex(r => r.value === 'api.vk.ru'))).toBe(1)
  })

  it('для остальных видов берёт текст правила', () => {
    expect(sortKey({ type: 'ip', value: '1.1.1.1' })).toBe('1.1.1.1')
    expect(sortKey({ type: 'cidr', value: '10.0.0.0/8' })).toBe('10.0.0.0/8')
    expect(sortKey({ type: 'geosite', value: 'youtube' })).toBe('geosite:youtube')
    expect(sortKey({ type: 'keyword', value: 'ads' })).toBe('keyword:ads')
    expect(sortKey({ type: 'app', value: 'firefox' })).toBe('app:firefox')
  })
})

describe('isGeo', () => {
  it('узнаёт только geoip и geosite', () => {
    expect(isGeo('geoip')).toBe(true)
    expect(isGeo('geosite')).toBe(true)
    expect(isGeo('domain')).toBe(false)
    expect(isGeo('ip')).toBe(false)
    expect(isGeo('app')).toBe(false)
    expect(isGeo('')).toBe(false)
  })
})

describe('geoTagCounts', () => {
  const state = geoState({
    geoip: geoInfo({ tags: [{ name: 'ru', count: 5 }, { name: 'private', count: 2 }] }),
    geosite: geoInfo({ tags: [{ name: 'youtube', count: 312 }, { name: 'github', count: 7 }] })
  })

  it('собирает счётчики по категориям обеих баз', () => {
    expect(geoTagCounts(state)).toEqual({
      'geoip:ru': 5,
      'geoip:private': 2,
      'geosite:youtube': 312,
      'geosite:github': 7
    })
  })

  it('без загруженной базы отдаёт пустой объект', () => {
    expect(geoTagCounts(null)).toEqual({})
  })

  it('не падает на базе без списка категорий', () => {
    const empty = { geoip: { present: true, url: 'x', tags: [] }, geosite: { present: true, url: 'x', tags: [] } }
    expect(geoTagCounts(empty)).toEqual({})
  })
})

describe('geoProblem', () => {
  it('молчит, когда правило не про базу или базы ещё нет', () => {
    expect(geoProblem(null, 'geoip', 'ru')).toBe('')
    expect(geoProblem(geoState(), 'domain', 'example.com')).toBe('')
  })

  it('сообщает, что база не загружена', () => {
    const state = geoState({
      geoip: geoInfo({ present: false }),
      geosite: geoInfo({ present: false })
    })
    expect(geoProblem(state, 'geoip', 'ru')).toBe('База geoip.dat не загружена — скачайте её в настройках')
    expect(geoProblem(state, 'geosite', 'youtube')).toBe('База geosite.dat не загружена — скачайте её в настройках')
  })

  it('сообщает, что категории нет', () => {
    const state = geoState({
      geoip: geoInfo({ tags: [{ name: 'ru', count: 5 }] }),
      geosite: geoInfo({ tags: [{ name: 'youtube', count: 312 }] })
    })
    expect(geoProblem(state, 'geoip', 'cn')).toBe('В geoip.dat нет категории «cn»')
    expect(geoProblem(state, 'geosite', 'missing')).toBe('В geosite.dat нет категории «missing»')
    expect(geoProblem(state, 'geosite', 'missing@cn')).toBe('В geosite.dat нет категории «missing@cn»')
  })

  it('ищет категорию по имени до @', () => {
    const state = geoState({
      geosite: geoInfo({ tags: [{ name: 'youtube', count: 312 }] })
    })
    expect(geoProblem(state, 'geosite', 'youtube@cn')).toBe('')
  })

  it('всё в порядке — пустая строка', () => {
    const state = geoState({
      geoip: geoInfo({ tags: [{ name: 'ru', count: 5 }] }),
      geosite: geoInfo({ tags: [{ name: 'youtube', count: 312 }] })
    })
    expect(geoProblem(state, 'geoip', 'ru')).toBe('')
    expect(geoProblem(state, 'geosite', 'youtube')).toBe('')
  })
})

describe('describeRule', () => {
  it('поясняет доменные виды', () => {
    expect(describeRule({ type: 'domain', value: 'example.com' }, undefined, {})).toBe(
      'Домен вместе со всеми поддоменами'
    )
    expect(describeRule({ type: 'zone', value: '.ru' }, undefined, {})).toBe(
      'Все имена, кончающиеся на .ru'
    )
    expect(describeRule({ type: 'full', value: 'example.com' }, undefined, {})).toBe(
      'Ровно это имя, без поддоменов'
    )
    expect(describeRule({ type: 'keyword', value: 'ads' }, undefined, {})).toBe(
      'Все имена, где встречается это слово'
    )
    expect(describeRule({ type: 'regexp', value: '^ads\\.' }, undefined, {})).toBe(
      'Имена по регулярному выражению'
    )
  })

  it('показывает размер категории из базы', () => {
    const counts = { 'geosite:youtube': 312, 'geoip:ru': 1234 }
    expect(describeRule({ type: 'geosite', value: 'youtube' }, undefined, counts)).toBe(
      'Сайты из базы: 312 записей'
    )
    expect(describeRule({ type: 'geoip', value: 'ru' }, undefined, counts)).toBe(
      `Сети из базы: 1${NBSP}234 подсетей`
    )
  })

  it('без счётчика пишет краткое пояснение', () => {
    expect(describeRule({ type: 'geosite', value: 'youtube' }, undefined, {})).toBe('Сайты из базы')
    expect(describeRule({ type: 'geoip', value: 'ru' }, undefined, {})).toBe('Сети из базы')
  })

  it('поясняет программу в обоих режимах', () => {
    expect(describeRule({ type: 'app', value: 'firefox' }, 'vpn_list', {})).toBe(
      'Все соединения программы «firefox» — через VPN'
    )
    expect(describeRule({ type: 'app', value: 'firefox' }, 'direct_list', {})).toBe(
      'Все соединения программы «firefox» — мимо туннеля'
    )
    expect(describeRule({ type: 'app', value: 'firefox' }, undefined, {})).toBe(
      'Все соединения программы «firefox» — мимо туннеля'
    )
  })

  it('отдельно поясняет UDP программы', () => {
    expect(describeRule({ type: 'app', value: 'vesktop/udp' }, 'vpn_list', {})).toBe(
      'UDP программы «vesktop» (голос, видео, игры) — через VPN'
    )
    expect(describeRule({ type: 'app', value: 'vesktop/udp' }, 'direct_list', {})).toBe(
      'UDP программы «vesktop» (голос, видео, игры) — мимо туннеля'
    )
  })

  it('поясняет адреса и подсети', () => {
    expect(describeRule({ type: 'cidr', value: '10.0.0.0/8' }, undefined, {})).toBe('Подсеть')
    expect(describeRule({ type: 'ip', value: '1.1.1.1' }, undefined, {})).toBe('Адрес')
  })
})

describe('splitTokens', () => {
  it('делит по запятым, точкам с запятой, пробелам и переводам строк', () => {
    expect(splitTokens('a, b;c\nd')).toEqual(['a', 'b', 'c', 'd'])
    expect(splitTokens('  a,,;\t b  ')).toEqual(['a', 'b'])
    expect(splitTokens('a\r\nb')).toEqual(['a', 'b'])
  })

  it('отбрасывает комментарии до конца строки', () => {
    expect(splitTokens('a # комментарий\n# целиком\nb # хвост')).toEqual(['a', 'b'])
    expect(splitTokens('foo#bar\nbaz')).toEqual(['foo', 'baz'])
    expect(splitTokens('# только комментарий')).toEqual([])
  })

  it('на пустом тексте отдаёт пустой список', () => {
    expect(splitTokens('')).toEqual([])
    expect(splitTokens('   \n\t\n')).toEqual([])
  })
})

describe('ruleTypeLabel', () => {
  it('переводит известные виды и оставляет неизвестный как есть', () => {
    expect(ruleTypeLabel('geoip')).toBe('Сети из базы')
    expect(ruleTypeLabel('app')).toBe('Программа')
    expect(ruleTypeLabel('unknown')).toBe('unknown')
  })
})
