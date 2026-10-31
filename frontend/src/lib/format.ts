// Форматирование чисел для экрана.

const UNITS = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ']

export function formatBytes(bytes: number): string {
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024
    unit++
  }
  const digits = unit === 0 || value >= 100 ? 0 : 1
  return `${value.toLocaleString('ru-RU', { maximumFractionDigits: digits, minimumFractionDigits: digits })} ${UNITS[unit]}`
}

// Скорость ниже килобайта в секунду — фоновый шум (keepalive), её не пишем.
export function formatRate(bytesPerSecond: number): string {
  if (bytesPerSecond < 1024) return ''
  return `${formatBytes(bytesPerSecond)}/с`
}

// 75 → «1:15», 3725 → «1:02:05».
export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(s % 60)}` : `${m}:${pad(s % 60)}`
}

// До 10 мс — с десятыми: там разница в полмиллисекунды заметна.
export function formatLatency(ms: number): string {
  const digits = ms < 10 ? 1 : 0
  return `${ms.toLocaleString('ru-RU', { minimumFractionDigits: digits, maximumFractionDigits: digits })} мс`
}

export function formatAge(iso: string, now = Date.now()): string {
  const days = Math.floor((now - new Date(iso).getTime()) / 86400000)
  if (days <= 0) return 'сегодня'
  if (days === 1) return 'вчера'
  return `${days} дн. назад`
}
