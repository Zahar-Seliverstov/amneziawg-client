<template>
  <section class="routing" aria-labelledby="routing-title">
    <header class="head">
      <h2 id="routing-title" class="head__title">Маршрутизация</h2>
      <div v-if="!bulk.open.value" class="head__actions">
        <button class="btn btn--ghost btn--icon" aria-label="Править текстом" title="Все правила одним текстом, по правилу на строку" @click="bulk.start()">
          <FileText aria-hidden="true" />
        </button>
        <button class="btn btn--ghost btn--icon" aria-label="Экспорт" title="Скачать правила файлом JSON" :disabled="!routing" @click="routing && downloadRules(routing)">
          <Download aria-hidden="true" />
        </button>
        <button class="btn btn--ghost btn--icon" aria-label="Импорт" title="Загрузить правила из файла" @click="fileInput?.click()">
          <Upload aria-hidden="true" />
        </button>
      </div>
    </header>
    <input ref="fileInput" type="file" accept="application/json,.json,.txt,.lst,.list,text/plain" hidden @change="onFile" />

    <ModeSwitch />

    <BulkEditor v-if="bulk.open.value" :editor="bulk" />
    <!-- Поле и список — одним блоком: строка подсказки под полем уже
         даёт нужный отступ. -->
    <div v-else>
      <RuleInput ref="input" />
      <RuleGroups :filter="input?.filter ?? ''" />
    </div>
  </section>
</template>

<script setup lang="ts">
// Раздел маршрутизации: собирает режим, поле ввода, список и правку
// текстом. Состояние правил — в stores/routing, здесь только компоновка и
// импорт файла, который касается сразу и режима, и правки текстом.
import { Download, FileText, Upload } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { useBulkEdit } from '../../composables/useBulkEdit'
import { downloadRules, parseRulesFile } from '../../lib/rulesFile'
import { ruleText } from '../../lib/rules'
import { useGeo } from '../../stores/geo'
import { useRouting } from '../../stores/routing'
import { notify, notifyError } from '../../stores/toasts'
import BulkEditor from './BulkEditor.vue'
import ModeSwitch from './ModeSwitch.vue'
import RuleGroups from './RuleGroups.vue'
import RuleInput from './RuleInput.vue'

const { routing, rules, load, setMode } = useRouting()
const bulk = useBulkEdit()

const input = ref<InstanceType<typeof RuleInput> | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)

// Импорт не применяется вслепую: правила из файла открываются в правке
// текстом — проверить и сохранить. Режим из выгрузки ставится сразу.
async function onFile(e: Event) {
  const target = e.target as HTMLInputElement
  const file = target.files?.[0]
  target.value = ''
  if (!file) return

  try {
    const { values, mode } = parseRulesFile(await file.text())
    if (!values.length) {
      notify('В файле нет ни одного правила')
      return
    }
    const have = new Set(rules.value.map(ruleText))
    bulk.start(values.filter(v => !have.has(v)))
    if (mode) await setMode(mode)
    notify(`Из файла правил: ${values.length} — проверьте и сохраните`, 'ok')
  } catch (err) {
    notifyError(err)
  }
}

onMounted(() => {
  load()
  useGeo().load()
})
</script>

<style scoped>
.routing {
  display: grid;
  gap: 16px;
  min-width: 0;
}

.head {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 32px;
}

.head__title {
  flex: 1;
  font-size: 16px;
  font-weight: 600;
}

.head__actions {
  display: flex;
  gap: 2px;
  margin-right: -6px;
}
</style>
