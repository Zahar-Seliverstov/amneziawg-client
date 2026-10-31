<template>
  <div>
    <div v-for="kind in KINDS" :key="kind" class="base">
      <div class="base__info">
        <span class="mono base__name">{{ kind }}.dat</span>
        <span class="base__what">{{ kind === 'geosite' ? 'сайты по категориям' : 'сети по странам' }}</span>
        <span class="base__state" :class="geo?.[kind]?.error ? 'bad' : 'muted'">{{ stateText(kind) }}</span>
      </div>
      <div class="row base__actions">
        <button class="btn btn--sm" :class="{ 'btn--busy': busy[kind] }" :disabled="!geo || busy[kind]" @click="refresh(kind)">
          {{ geo?.[kind]?.present ? 'Обновить' : 'Скачать' }}
        </button>
        <button class="btn btn--ghost btn--sm" :disabled="!geo || busy[kind]" @click="pickFile(kind)">Из файла…</button>
        <button class="btn btn--ghost btn--sm" :class="{ 'btn--primary': editingUrl[kind] }" :disabled="!geo" @click="toggleUrl(kind)">Источник</button>
      </div>
      <form v-if="editingUrl[kind]" class="row base__url" @submit.prevent="refresh(kind, urls[kind])">
        <input v-model="urls[kind]" class="input mono" spellcheck="false" :aria-label="`Адрес ${kind}.dat`" />
        <button class="btn btn--primary btn--sm" type="submit" :disabled="busy[kind]">Скачать</button>
      </form>
    </div>

    <input ref="fileInput" type="file" accept=".dat,application/octet-stream" hidden @change="onFile" />
  </div>
</template>

<script setup lang="ts">
// Базы geosite.dat и geoip.dat: состояние, обновление, загрузка из файла и
// смена адреса, откуда их качать.
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
  if (busy[kind] || info.updating) return 'скачивается…'
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
.base {
  padding: 10px 12px;
  border: 1px solid var(--line);
  border-radius: 4px;
}

.base + .base {
  margin-top: 8px;
}

.base__info {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 12px;
}

.base__name {
  font-weight: 600;
}

.base__what {
  color: var(--muted);
  font-size: 13px;
}

.base__state {
  flex-basis: 100%;
  font-size: 13px;
}

.base__actions {
  gap: 4px;
  margin-top: 8px;
}

.base__url {
  margin-top: 8px;
}
</style>
