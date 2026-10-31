// Состояние баз geoip/geosite: его читают и поле правил (подсказки,
// проверка категорий), и настройки (обновление баз).

import { computed, shallowRef } from 'vue'
import { api } from '../api/client'
import type { GeoKind, GeoState } from '../api/types'
import { geoTagCounts } from '../lib/rules'

const geo = shallowRef<GeoState | null>(null)
const counts = computed(() => geoTagCounts(geo.value))
let loading: Promise<void> | null = null

// Загрузка один раз на всё приложение, сколько бы мест её ни попросило.
function load(force = false): Promise<void> {
  if (loading && !force) return loading
  loading = api
    .getGeo()
    .then(state => {
      geo.value = state
    })
    .catch(e => {
      loading = null
      console.error('Базы geoip/geosite не прочитаны:', e)
    })
  return loading
}

async function update(kind: GeoKind, url = '') {
  geo.value = await api.updateGeo(kind, url)
}

async function upload(kind: GeoKind, file: File) {
  geo.value = await api.uploadGeo(kind, file)
}

export function useGeo() {
  return { geo, counts, load, update, upload }
}
