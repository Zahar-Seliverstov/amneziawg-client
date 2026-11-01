<template>
  <div class="picker" role="dialog" aria-label="Выбор программы">
    <div class="row picker__head">
      <div class="field-box grow picker__search">
        <Search aria-hidden="true" />
        <input
          ref="search"
          v-model="query"
          placeholder="Найти программу"
          spellcheck="false"
          autocomplete="off"
          aria-label="Найти программу"
          @keydown.esc="emit('close')"
        />
      </div>
      <button class="btn btn--ghost btn--icon btn--sm" :disabled="loading" aria-label="Обновить список" title="Обновить список" @click="load">
        <RefreshCw :class="{ spin: loading }" aria-hidden="true" />
      </button>
      <button class="btn btn--ghost btn--icon btn--sm" aria-label="Закрыть" title="Закрыть · Esc" @click="emit('close')">
        <X aria-hidden="true" />
      </button>
    </div>

    <p class="picker__note">{{ note }}</p>

    <div class="picker__list">
      <template v-for="section in sections" :key="section.title">
        <h4 v-if="section.apps.length" class="picker__section">{{ section.title }}</h4>
        <button
          v-for="app in section.apps"
          :key="app.name"
          class="app"
          :class="{ 'app--added': isAdded(app) }"
          :disabled="isAdded(app) || pending !== ''"
          @click="pick(app)"
        >
          <img
            v-if="app.icon && !broken.has(app.icon)"
            class="app__icon"
            :src="`/api/apps/icon/${app.icon}`"
            alt=""
            loading="lazy"
            @error="broken.add(app.icon!)"
          />
          <span v-else class="app__icon app__icon--letter" aria-hidden="true">{{ app.title.charAt(0).toUpperCase() }}</span>
          <span class="grow ellipsis">
            {{ app.title }}
            <span class="app__name ellipsis">app:{{ app.name }}<template v-if="app.hint"> · {{ app.hint }}</template></span>
          </span>
          <span class="app__action">
            <LoaderCircle v-if="pending === app.name" class="spin" aria-hidden="true" />
            <template v-else>{{ isAdded(app) ? 'В списке' : 'Добавить' }}</template>
          </span>
        </button>
      </template>

      <p v-if="catalog && !catalog.supported" class="empty">
        Здесь служба не может показать список программ.
        Правило можно вписать вручную: app:имя — по имени процесса.
      </p>
      <p v-else-if="!loading && !shown.length" class="empty">
        {{ catalog?.apps.length ? 'Такой программы нет.' : 'Программ не нашлось.' }}
        Можно вписать вручную: app:имя — по имени процесса или слову из командной строки.
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
// Выбор программы для правила app: из запущенных и установленных.
import { LoaderCircle, RefreshCw, Search, X } from '@lucide/vue'
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { api } from '../../api/client'
import type { AppCatalog, AppInfo } from '../../api/types'
import { useRouting } from '../../stores/routing'
import { notify, notifyError } from '../../stores/toasts'

const emit = defineEmits<{ close: [] }>()

const { mode, savedKeys, apply } = useRouting()

const catalog = ref<AppCatalog | null>(null)
const loading = ref(false)
const pending = ref('')
const query = ref('')
const search = ref<HTMLInputElement | null>(null)
const broken = reactive(new Set<string>())

const note = computed(() =>
  mode.value === 'vpn_list'
    ? 'Выбранная программа пойдёт через VPN целиком — все её соединения.'
    : 'Выбранная программа пойдёт мимо туннеля целиком, как без VPN.'
)

function isAdded(app: AppInfo) {
  return savedKeys.value.has(`app:${app.name}`)
}

const shown = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const apps = catalog.value?.apps || []
  if (!needle) return apps
  return apps.filter(a => a.title.toLowerCase().includes(needle) || a.name.includes(needle))
})

const sections = computed(() => [
  { title: 'Работают сейчас', apps: shown.value.filter(a => a.running > 0) },
  { title: 'Установлены', apps: shown.value.filter(a => a.running === 0) }
])

async function pick(app: AppInfo) {
  if (isAdded(app) || pending.value) return
  pending.value = app.name
  const result = await apply([`app:${app.name}`])
  pending.value = ''
  if (result && !result.rejected.length) {
    notify(`${app.title} — ${mode.value === 'vpn_list' ? 'через VPN' : 'мимо туннеля'}`, 'ok')
  }
}

async function load() {
  loading.value = true
  try {
    catalog.value = await api.getApps()
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  load()
  nextTick(() => search.value?.focus())
})
</script>

<style scoped>
.picker__head {
  gap: 2px;
  padding: 6px;
}

.picker__search {
  height: 32px;
  padding-left: 10px;
}

.picker__note {
  padding: 4px 12px 0;
  color: var(--muted);
  font-size: 13px;
}

.picker__list {
  max-height: min(380px, 55vh);
  overflow-y: auto;
  padding: 0 4px 6px;
}

.picker__section {
  padding: 12px 8px 4px;
  color: var(--faint);
  font-size: 12px;
  font-weight: 500;
}

.app {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  min-height: 40px;
  padding: 4px 8px;
  border-radius: 6px;
  text-align: left;
  transition: background-color var(--t);
}

.app:hover:not(:disabled) {
  background: var(--surface);
}

.app--added:disabled {
  opacity: 0.5;
}

.app__icon {
  width: 20px;
  height: 20px;
  flex: none;
}

.app__icon--letter {
  display: grid;
  place-items: center;
  border-radius: 5px;
  background: var(--surface);
  color: var(--muted);
  font-size: 11px;
  font-weight: 600;
}

.app__name {
  display: block;
  color: var(--faint);
  font-size: 12px;
}

.app__action {
  color: var(--muted);
  font-size: 13px;
  white-space: nowrap;
}

.empty {
  padding: 10px 8px;
}
</style>
