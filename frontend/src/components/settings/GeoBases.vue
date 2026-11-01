<template>
  <div class="rows">
    <div v-for="kind in KINDS" :key="kind" class="line line--base">
      <div class="line__text">
        {{ kind }}.dat
        <span class="line__sub" :class="{ error: geo?.[kind]?.error }">{{ kind === 'geosite' ? 'Сайты по категориям' : 'Сети по странам' }} · {{ stateText(kind) }}</span>
      </div>
      <div class="actions">
        <button class="btn btn--sm" :class="{ 'btn--busy': busy[kind] }" :disabled="!geo || isBusy(kind)" @click="refresh(kind)">
          <LoaderCircle v-if="isBusy(kind)" class="spin" aria-hidden="true" />
          {{ geo?.[kind]?.present ? 'Обновить' : 'Скачать' }}
        </button>
        <button class="btn btn--ghost btn--icon btn--sm" :disabled="!geo || isBusy(kind)" aria-label="Загрузить из файла" title="Загрузить из файла" @click="pickFile(kind)">
          <Upload aria-hidden="true" />
        </button>
        <button
          class="btn btn--ghost btn--icon btn--sm"
          :class="{ 'btn--on': editingUrl[kind] }"
          :aria-expanded="editingUrl[kind]"
          :disabled="!geo"
          aria-label="Адрес, откуда скачивать"
          title="Адрес, откуда скачивать"
          @click="toggleUrl(kind)"
        >
          <Link aria-hidden="true" />
        </button>
      </div>
      <form v-if="editingUrl[kind]" class="row url" @submit.prevent="refresh(kind, urls[kind])">
        <input v-model="urls[kind]" class="input" spellcheck="false" :aria-label="`Адрес ${kind}.dat`" />
        <button class="btn btn--primary" type="submit" :disabled="isBusy(kind)">Скачать</button>
      </form>
    </div>
    <input ref="fileInput" type="file" accept=".dat,application/octet-stream" hidden @change="onFile" />
  </div>
</template>

<script setup lang="ts">
// Базы geosite.dat и geoip.dat: состояние, обновление, загрузка из файла и
// смена адреса, откуда их качать.
import { Link, LoaderCircle, Upload } from '@lucide/vue'
import { reactive, ref } from 'vue'
import type { GeoKind } from '../../api/types'
import { formatAge } from '../../lib/format'
import { useGeo } from '../../stores/geo'
import { notify, notifyError } from '../../stores/toasts'

const KINDS: GeoKind[] = ['geosite', 'geoip']

const { geo, load, update, upload } = useGeo()

const busy = reactive<Record<GeoKind, boolean>>({ geoip: false, geosite: false })
const editingUrl = reactive<Record<GeoKind, boolean>>({ geoip: false, geosite: false })
const urls = reactive<Record<GeoKind, string>>({ geoip: '', geosite: '' })
const fileInput = ref<HTMLInputElement | null>(null)
let fileKind: GeoKind = 'geosite'

function stateText(kind: GeoKind) {
  const info = geo.value?.[kind]
  if (!info) return '…'
  if (isBusy(kind)) return 'скачивается…'
  if (info.error && !info.present) return info.error
  if (!info.present) return 'не загружена'

  const parts = [
    `${info.tags.length.toLocaleString('ru-RU')} категорий`,
    `${((info.size || 0) / 1048576).toFixed(1)} МБ`,
    info.updated ? `обновлена ${formatAge(info.updated)}` : ''
  ].filter(Boolean)
  if (info.error) parts.push('последнее обновление не удалось')
  return parts.join(' · ')
}

function isBusy(kind: GeoKind) {
  return busy[kind] || Boolean(geo.value?.[kind]?.updating)
}

function toggleUrl(kind: GeoKind) {
  editingUrl[kind] = !editingUrl[kind]
  if (editingUrl[kind]) urls[kind] = geo.value?.[kind]?.url || ''
}

async function withBusy(kind: GeoKind, action: () => Promise<void>, done: string) {
  busy[kind] = true
  try {
    await action()
    notify(done, 'ok')
    return true
  } catch (e) {
    notifyError(e)
    await load(true)
    return false
  } finally {
    busy[kind] = false
  }
}

async function refresh(kind: GeoKind, url = '') {
  if (await withBusy(kind, () => update(kind, url), `${kind}.dat обновлена`)) editingUrl[kind] = false
}

function pickFile(kind: GeoKind) {
  fileKind = kind
  fileInput.value?.click()
}

async function onFile(e: Event) {
  const target = e.target as HTMLInputElement
  const file = target.files?.[0]
  target.value = ''
  if (file) await withBusy(fileKind, () => upload(fileKind, file), `${fileKind}.dat загружена из файла`)
}
</script>

<style scoped>
/* Строки — те же, что в остальных настройках (.line из SettingsPanel). */
.line--base {
  flex-wrap: wrap;
}

.actions {
  display: flex;
  align-items: center;
  gap: 2px;
}

.url {
  flex-basis: 100%;
}
</style>
