// Выгрузка и загрузка списка правил файлом.

import type { RoutingConfig, RoutingMode } from '../api/types'
import { ruleText } from './rules'

export function downloadRules(routing: RoutingConfig) {
  const body = JSON.stringify({ mode: routing.mode, rules: routing.rules }, null, 2)
  const url = URL.createObjectURL(new Blob([body], { type: 'application/json' }))
  const a = document.createElement('a')
  a.href = url
  a.download = `awg-routing-${new Date().toISOString().slice(0, 10)}.json`
  a.click()
  URL.revokeObjectURL(url)
}

export interface ParsedRulesFile {
  values: string[]
  mode: RoutingMode | null
}

// Понимает свою же выгрузку (JSON с mode и rules), список строк в JSON и
// простой текст по правилу на строку — как в списках для других клиентов.
export function parseRulesFile(text: string): ParsedRulesFile {
  let data: any
  try {
    data = JSON.parse(text)
  } catch {
    return { values: text.split(/\r?\n/).map(l => l.replace(/#.*/, '').trim()).filter(Boolean), mode: null }
  }

  if (!data || !Array.isArray(data.rules)) throw new Error('В файле нет списка правил')

  const values = data.rules
    .map((r: any) => (typeof r === 'string' ? r : r?.type && r?.value ? ruleText(r) : r?.value))
    .filter((v: unknown): v is string => typeof v === 'string' && v.trim() !== '')
  const mode = data.mode === 'vpn_list' || data.mode === 'direct_list' ? data.mode : null
  return { values, mode }
}
