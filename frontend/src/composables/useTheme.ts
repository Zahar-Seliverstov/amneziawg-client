import { ref, watch } from 'vue'

// Тема оформления: как в системе, светлая или тёмная.
//
// Выбор — свойство этого браузера, а не службы: на телефоне и на компьютере
// человеку может хотеться разного. Поэтому он хранится в localStorage.
// «Как в системе» разрешается здесь же в конкретную тему: так в CSS один
// набор переменных на тему, без повтора под prefers-color-scheme.

export type ThemeChoice = 'system' | 'light' | 'dark'

const KEY = 'awg-theme'
const CHOICES: ThemeChoice[] = ['system', 'light', 'dark']

function stored(): ThemeChoice {
  try {
    const value = localStorage.getItem(KEY) as ThemeChoice | null
    return value && CHOICES.includes(value) ? value : 'system'
  } catch {
    // Хранилище бывает закрыто (приватный режим) — тогда просто как в системе.
    return 'system'
  }
}

const choice = ref<ThemeChoice>(stored())
const systemDark = window.matchMedia('(prefers-color-scheme: dark)')

function apply() {
  const dark = choice.value === 'dark' || (choice.value === 'system' && systemDark.matches)
  document.documentElement.dataset.theme = dark ? 'dark' : 'light'
}

// Тему ставим до монтирования приложения — иначе первый кадр мигнёт
// светлым у того, кто выбрал тёмную.
export function initTheme() {
  apply()
  systemDark.addEventListener('change', apply)
  watch(choice, value => {
    try {
      localStorage.setItem(KEY, value)
    } catch {}
    apply()
  })
}

export function useTheme() {
  return { choice }
}
