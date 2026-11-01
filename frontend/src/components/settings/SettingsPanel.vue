<template>
  <div class="settings">
    <header class="settings__head">
      <button class="btn btn--ghost btn--icon" aria-label="Назад" title="Назад · Esc" @click="$emit('back')">
        <ArrowLeft aria-hidden="true" />
      </button>
      <h1>Настройки</h1>
    </header>

    <section class="group" aria-labelledby="set-look">
      <h2 id="set-look" class="group__title">Оформление</h2>
      <div class="rows">
        <div class="line line--wrap">
          <div class="line__text">Тема<span class="line__sub">Запоминается в этом браузере</span></div>
          <Segmented v-model="theme" :options="THEMES" label="Тема оформления" />
        </div>
      </div>
    </section>

    <section class="group" aria-labelledby="set-start">
      <h2 id="set-start" class="group__title">Запуск</h2>
      <div class="rows">
        <label class="line">
          <span class="line__text">
            Подключаться автоматически
            <span class="line__sub">К выбранному подключению, сразу при запуске системы</span>
          </span>
          <LoaderCircle v-if="saving" class="spin faint" aria-hidden="true" />
          <input v-model="autoconnect" class="switch" type="checkbox" role="switch" :disabled="saving || !loaded" @change="save" />
        </label>
      </div>
    </section>

    <section class="group" aria-labelledby="set-geo">
      <h2 id="set-geo" class="group__title">Базы наборов</h2>
      <GeoBases />
      <p class="group__note">Из них берутся правила geosite: и geoip:. Служба обновляет базы сама раз в неделю.</p>
    </section>

    <section class="group" aria-labelledby="set-keys">
      <h2 id="set-keys" class="group__title">Клавиши</h2>
      <dl class="rows">
        <div v-for="k in KEYS" :key="k.what" class="line">
          <dt class="line__text">{{ k.what }}</dt>
          <dd><kbd v-for="key in k.keys" :key="key">{{ key }}</kbd></dd>
        </div>
      </dl>
    </section>

    <section v-if="version" class="group" aria-labelledby="set-about">
      <h2 id="set-about" class="group__title">О программе</h2>
      <div class="rows">
        <div class="line">
          <span class="line__text">Версия</span>
          <span class="muted num">{{ version }}</span>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
// Настройки в духе системных: группы без рамок, в каждой строке подпись
// слева и переключатель справа, строки разделены тонкой линией.
import { ArrowLeft, LoaderCircle } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { api } from '../../api/client'
import { useTheme, type ThemeChoice } from '../../composables/useTheme'
import { useGeo } from '../../stores/geo'
import { notifyError } from '../../stores/toasts'
import Segmented, { type SegmentedOption } from '../ui/Segmented.vue'
import GeoBases from './GeoBases.vue'

defineProps<{ version: string }>()
defineEmits<{ back: [] }>()

const THEMES: SegmentedOption<ThemeChoice>[] = [
  { value: 'system', title: 'Системная' },
  { value: 'light', title: 'Светлая' },
  { value: 'dark', title: 'Тёмная' }
]

const KEYS = [
  { what: 'Перейти к полю правил', keys: ['/'] },
  { what: 'Подключить или отключить', keys: ['C'] },
  { what: 'Открыть или закрыть настройки', keys: [','] },
  { what: 'Назад, закрыть подсказки и окна', keys: ['Esc'] },
  { what: 'Сохранить форму', keys: ['Ctrl', 'Enter'] }
]

const { choice: theme } = useTheme()

const autoconnect = ref(false)
const loaded = ref(false)
const saving = ref(false)

async function save() {
  saving.value = true
  try {
    await api.setSettings({ autoconnect: autoconnect.value })
  } catch (e) {
    autoconnect.value = !autoconnect.value
    notifyError(e)
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  useGeo().load()
  try {
    autoconnect.value = (await api.getSettings()).autoconnect
    loaded.value = true
  } catch (e) {
    notifyError(e)
  }
})
</script>

<style scoped>
.settings {
  display: grid;
  gap: 32px;
}

.settings__head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: -8px;
}

h1 {
  font-size: 20px;
  font-weight: 600;
}

.group {
  display: grid;
  gap: 4px;
  min-width: 0;
}

.group__title {
  color: var(--muted);
  font-size: 13px;
  font-weight: 500;
}

.group__note {
  color: var(--faint);
  font-size: 13px;
}

/* Строки группы и общие для строк правила: GeoBases строит такие же. */
.rows {
  display: grid;
}

:deep(.line) {
  display: flex;
  align-items: center;
  gap: 16px;
  min-height: 48px;
  padding: 10px 0;
}

:deep(.line + .line) {
  border-top: 1px solid var(--line);
}

:deep(.line__text) {
  flex: 1;
  min-width: 0;
}

:deep(.line__sub) {
  display: block;
  color: var(--muted);
  font-size: 13px;
}

label.line {
  cursor: pointer;
}

dd {
  display: flex;
  gap: 4px;
}

@media (max-width: 640px) {
  .line--wrap {
    flex-wrap: wrap;
  }

  .line--wrap :deep(.seg) {
    display: flex;
    width: 100%;
  }

  .line--wrap :deep(.seg__btn) {
    flex: 1;
  }
}
</style>
