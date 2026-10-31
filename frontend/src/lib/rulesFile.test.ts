import { describe, it, expect } from 'vitest'
import { parseRulesFile } from './rulesFile'

describe('parseRulesFile: своя выгрузка', () => {
  it('возвращает режим и правила с приставками, где они нужны', () => {
    const file = JSON.stringify({
      mode: 'vpn_list',
      rules: [
        { type: 'domain', value: 'example.com' },
        { type: 'zone', value: '.ru' },
        { type: 'full', value: 'static.example.com' },
        { type: 'keyword', value: 'ads' },
        { type: 'regexp', value: '^ads\\.' },
        { type: 'geosite', value: 'youtube' },
        { type: 'geoip', value: 'ru' },
        { type: 'app', value: 'firefox' },
        { type: 'ip', value: '1.1.1.1' },
        { type: 'cidr', value: '10.0.0.0/8' }
      ]
    })

    expect(parseRulesFile(file)).toEqual({
      values: [
        'example.com',
        '.ru',
        'full:static.example.com',
        'keyword:ads',
        'regexp:^ads\\.',
        'geosite:youtube',
        'geoip:ru',
        'app:firefox',
        '1.1.1.1',
        '10.0.0.0/8'
      ],
      mode: 'vpn_list'
    })
  })

  it('берёт режим direct_list', () => {
    const file = JSON.stringify({ mode: 'direct_list', rules: [{ type: 'domain', value: 'example.com' }] })
    expect(parseRulesFile(file)).toEqual({ values: ['example.com'], mode: 'direct_list' })
  })

  it('пропускает объекты без значения и нестроковые элементы', () => {
    const file = JSON.stringify({
      rules: [
        { type: 'domain', value: 'example.com' },
        { value: 'bare' },
        { type: 'geosite', value: 'youtube' },
        { type: 'domain' },
        { value: '' },
        { type: 'domain', value: '   ' },
        null,
        42
      ]
    })

    expect(parseRulesFile(file)).toEqual({
      values: ['example.com', 'bare', 'geosite:youtube'],
      mode: null
    })
  })

  it('пустой список правил допустим', () => {
    expect(parseRulesFile('{"mode":"vpn_list","rules":[]}')).toEqual({ values: [], mode: 'vpn_list' })
    expect(parseRulesFile('{"rules":[]}')).toEqual({ values: [], mode: null })
  })
})

describe('parseRulesFile: rules строками', () => {
  it('оставляет строки как есть и отбрасывает пустые', () => {
    const file = JSON.stringify({
      mode: 'direct_list',
      rules: ['example.com', 'geosite:youtube', '', '   ']
    })

    expect(parseRulesFile(file)).toEqual({
      values: ['example.com', 'geosite:youtube'],
      mode: 'direct_list'
    })
  })
})

describe('parseRulesFile: простой текст', () => {
  it('разбирает по строке и убирает комментарии', () => {
    const text = 'example.com\n# комментарий\ngeoip:ru\n\n  keyword:ads  # хвост\n'
    expect(parseRulesFile(text)).toEqual({
      values: ['example.com', 'geoip:ru', 'keyword:ads'],
      mode: null
    })
  })

  it('понимает переводы строк CRLF', () => {
    expect(parseRulesFile('a.com\r\nb.com # хвост\r\n')).toEqual({
      values: ['a.com', 'b.com'],
      mode: null
    })
  })

  it('на пустом тексте и одних комментариях отдаёт пустой список', () => {
    expect(parseRulesFile('')).toEqual({ values: [], mode: null })
    expect(parseRulesFile('# только комментарий\n   \n')).toEqual({ values: [], mode: null })
  })
})

describe('parseRulesFile: ошибки и режим', () => {
  it('бросает исключение, если в JSON нет списка правил', () => {
    expect(() => parseRulesFile('{}')).toThrow('В файле нет списка правил')
    expect(() => parseRulesFile('{"mode":"vpn_list"}')).toThrow('В файле нет списка правил')
    expect(() => parseRulesFile('[]')).toThrow('В файле нет списка правил')
    expect(() => parseRulesFile('null')).toThrow('В файле нет списка правил')
    expect(() => parseRulesFile('{"rules":"example.com"}')).toThrow('В файле нет списка правил')
  })

  it('неизвестный или неверный по регистру режим — null', () => {
    expect(parseRulesFile('{"mode":"bogus","rules":[]}')).toEqual({ values: [], mode: null })
    expect(parseRulesFile('{"mode":"VPN_LIST","rules":[]}')).toEqual({ values: [], mode: null })
    expect(parseRulesFile('{"mode":null,"rules":[]}')).toEqual({ values: [], mode: null })
  })
})
