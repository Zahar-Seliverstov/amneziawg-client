<template>
  <div class="settings">
    <section>
      <SectionHead title="Запуск" note="Что делать при включении компьютера" />
      <label class="check">
        <input v-model="autoconnect" type="checkbox" :disabled="saving || !loaded" @change="save" />
        <span>
          Подключаться автоматически
          <span class="faint sub">к выбранному подключению, сразу при запуске системы</span>
        </span>
      </label>
    </section>

    <section>
      <SectionHead title="Базы наборов" note="Источник правил geosite:… и geoip:… · служба обновляет их сама раз в неделю" />
      <GeoBases />
    </section>

    <section>
      <SectionHead title="Клавиши" />
      <dl class="keys">
        <dt><kbd>/</kbd></dt><dd>перейти к полю правил</dd>
        <dt><kbd>C</kbd></dt><dd>подключить или отключить</dd>
        <dt><kbd>,</kbd></dt><dd>открыть или закрыть настройки</dd>
        <dt><kbd>Esc</kbd></dt><dd>назад, закрыть подсказки</dd>
        <dt><kbd>Ctrl+Enter</kbd></dt><dd>сохранить форму</dd>
      </dl>
    </section>
  </div>
</template>

<script setup lang="ts">
// Настройки: автоподключение, базы наборов, справка по клавишам.
import { onMounted, ref } from 'vue'
import { api } from '../../api/client'
import { useGeo } from '../../stores/geo'
import { notifyError } from '../../stores/toasts'
import SectionHead from '../SectionHead.vue'
import GeoBases from './GeoBases.vue'

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
  gap: 28px;
  max-width: 680px;
}

.sub {
  display: block;
  font-size: 13px;
}

.keys {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 8px 16px;
  align-items: center;
}

.keys dd {
  color: var(--muted);
}
</style>
