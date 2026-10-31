<template>
  <div class="picker" role="dialog" aria-label="Выбор программы">
    <div class="row picker__head">
      <input
        ref="search"
        v-model="query"
        class="input"
        placeholder="Найти программу"
        spellcheck="false"
        autocomplete="off"
        aria-label="Найти программу"
        @keydown.esc="emit('close')"
      />
      <button class="btn btn--ghost btn--sm" :class="{ 'btn--busy': loading }" @click="load">Обновить</button>
      <button class="btn btn--ghost btn--sm" title="Esc" @click="emit('close')">Закрыть</button>
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
            <span class="faint mono">app:{{ app.name }}<template v-if="app.hint"> · {{ app.hint }}</template></span>
          </span>
          <span v-if="app.running" class="ok" :title="`Процессов: ${app.running}`">●</span>
          <span class="app__action">{{ isAdded(app) ? 'в списке' : pending === app.name ? '…' : 'Добавить' }}</span>
        </button>
      </template>

      <p v-if="catalog && !catalog.supported" class="empty">
        Правила для программ работают только на Linux.
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
  padding: 8px;
  border-bottom: 1px solid var(--line);
}

.picker__note {
  padding: 6px 10px;
  color: var(--faint);
  font-size: var(--fs-s);
}

.picker__list {
  max-height: 360px;
  overflow-y: auto;
  padding-bottom: 6px;
}

.picker__section {
  padding: 8px 10px 4px;
  color: var(--muted);
  font-size: var(--fs-s);
  font-weight: normal;
}

.app {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  height: 30px;
  padding: 0 10px;
  text-align: left;
}

.app:hover:not(:disabled) {
  background: var(--sel);
}

.app--added:disabled {
  opacity: 0.6;
}

.app__icon {
  width: 18px;
  height: 18px;
  flex: none;
}

.app__icon--letter {
  display: grid;
  place-items: center;
  border: 1px solid var(--line-strong);
  color: var(--muted);
  font-size: 11px;
}

.app__action {
  min-width: 56px;
  color: var(--accent);
  text-align: right;
}

.empty {
  padding: 8px 10px;
}
</style>
