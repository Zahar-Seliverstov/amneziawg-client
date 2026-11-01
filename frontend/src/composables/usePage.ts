import { ref } from 'vue'

// Какой экран открыт: главный или настройки.
//
// Настройки получают свою запись в истории (#settings): на телефоне жест
// «назад» должен закрывать их, а не уводить со страницы. По той же причине
// «назад» внутри интерфейса — это history.back(), если настройки открыли мы.

export type Page = 'main' | 'settings'

const fromHash = (): Page => (location.hash === '#settings' ? 'settings' : 'main')

const page = ref<Page>(fromHash())

window.addEventListener('popstate', () => {
  page.value = fromHash()
})

function open(next: Page) {
  if (next === page.value) return
  if (next === 'settings') {
    history.pushState({ settings: true }, '', '#settings')
    page.value = 'settings'
  } else if (history.state?.settings) {
    history.back()
  } else {
    // Страницу открыли сразу на настройках — назад некуда, просто меняем адрес.
    history.replaceState(null, '', location.pathname + location.search)
    page.value = 'main'
  }
}

export function usePage() {
  return { page, open, toggle: () => open(page.value === 'settings' ? 'main' : 'settings') }
}
