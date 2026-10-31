import { describe, it, expect } from 'vitest'
import { formatBytes, formatRate, formatDuration, formatLatency, formatAge } from './format'

const NBSP = '\u00A0'

describe('formatBytes', () => {
  it('пишет байты без дробной части', () => {
    expect(formatBytes(0)).toBe('0 Б')
    expect(formatBytes(512)).toBe('512 Б')
    expect(formatBytes(1023)).toBe(`1${NBSP}023 Б`)
  })

  it('переходит на КБ, МБ и ТБ', () => {
    expect(formatBytes(1024)).toBe('1,0 КБ')
    expect(formatBytes(1536)).toBe('1,5 КБ')
    expect(formatBytes(10 * 1024 ** 2)).toBe('10,0 МБ')
    expect(formatBytes(1024 ** 4)).toBe('1,0 ТБ')
  })

  it('округляет до целого при значении от 100', () => {
    expect(formatBytes(99 * 1024)).toBe('99,0 КБ')
    expect(formatBytes(100 * 1024)).toBe('100 КБ')
  })

  it('не уходит выше ТБ', () => {
    expect(formatBytes(1024 ** 5)).toBe(`1${NBSP}024 ТБ`)
  })
})

describe('formatRate', () => {
  it('молчит ниже килобайта в секунду', () => {
    expect(formatRate(0)).toBe('')
    expect(formatRate(512)).toBe('')
    expect(formatRate(1023)).toBe('')
    expect(formatRate(-1)).toBe('')
  })

  it('от килобайта в секунду добавляет «/с»', () => {
    expect(formatRate(1024)).toBe('1,0 КБ/с')
    expect(formatRate(1536)).toBe('1,5 КБ/с')
    expect(formatRate(2 * 1024)).toBe('2,0 КБ/с')
    expect(formatRate(1024 ** 2)).toBe('1,0 МБ/с')
  })
})

describe('formatDuration', () => {
  it('без часов пишет минуты и секунды', () => {
    expect(formatDuration(0)).toBe('0:00')
    expect(formatDuration(9)).toBe('0:09')
    expect(formatDuration(59.9)).toBe('0:59')
    expect(formatDuration(60)).toBe('1:00')
    expect(formatDuration(75)).toBe('1:15')
    expect(formatDuration(3599)).toBe('59:59')
  })

  it('с часами пишет часы, минуты и секунды', () => {
    expect(formatDuration(3600)).toBe('1:00:00')
    expect(formatDuration(3661)).toBe('1:01:01')
    expect(formatDuration(3725)).toBe('1:02:05')
    expect(formatDuration(90061)).toBe('25:01:01')
  })

  it('отрицательное время считает нулём', () => {
    expect(formatDuration(-5)).toBe('0:00')
    expect(formatDuration(-0.5)).toBe('0:00')
  })
})

describe('formatLatency', () => {
  it('меньше 10 мс пишет с одним знаком после запятой', () => {
    expect(formatLatency(0)).toBe('0,0 мс')
    expect(formatLatency(5.25)).toBe('5,3 мс')
    expect(formatLatency(9.99)).toBe('10,0 мс')
  })

  it('от 10 мс округляет до целого', () => {
    expect(formatLatency(10)).toBe('10 мс')
    expect(formatLatency(42.6)).toBe('43 мс')
    expect(formatLatency(100.4)).toBe('100 мс')
  })
})

describe('formatAge', () => {
  const now = Date.UTC(2026, 0, 15, 12, 0, 0)
  const ago = (ms: number) => new Date(now - ms).toISOString()
  const day = 86400000

  it('сегодня — от нуля до суток назад', () => {
    expect(formatAge(ago(0), now)).toBe('сегодня')
    expect(formatAge(ago(23 * 3600000), now)).toBe('сегодня')
  })

  it('вчера — от суток до двух суток назад', () => {
    expect(formatAge(ago(day), now)).toBe('вчера')
    expect(formatAge(ago(25 * 3600000), now)).toBe('вчера')
    expect(formatAge(ago(47 * 3600000), now)).toBe('вчера')
  })

  it('дальше двух суток пишет число дней', () => {
    expect(formatAge(ago(2 * day), now)).toBe('2 дн. назад')
    expect(formatAge(ago(3 * day), now)).toBe('3 дн. назад')
    expect(formatAge(ago(10 * day), now)).toBe('10 дн. назад')
  })

  it('дата в будущем считается сегодняшней', () => {
    expect(formatAge(ago(-day), now)).toBe('сегодня')
  })
})
