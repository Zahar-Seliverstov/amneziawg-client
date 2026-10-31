<template>
  <section>
    <SectionHead title="Маршрутизация" note="Что идёт через VPN, а что — напрямую">
      <template v-if="!bulk.open.value">
        <button class="btn btn--sm" title="Все правила одним текстом, по правилу на строку" @click="bulk.start()">Править текстом</button>
        <button class="btn btn--sm" title="Скачать правила файлом JSON" :disabled="!routing" @click="routing && downloadRules(routing)">Экспорт</button>
        <button class="btn btn--sm" title="Загрузить правила из файла" @click="fileInput?.click()">Импорт…</button>
      </template>
    </SectionHead>
    <input ref="fileInput" type="file" accept="application/json,.json,.txt,.lst,.list,text/plain" hidden @change="onFile" />

    <ModeSwitch />

    <BulkEditor v-if="bulk.open.value" :editor="bulk" />
    <template v-else>
      <RuleInput ref="input" />
      <RuleGroups :filter="input?.filter ?? ''" />
    </template>
  </section>
</template>

<script setup lang="ts">
// Раздел маршрутизации: собирает режим, поле ввода, список и правку
// текстом. Состояние правил — в stores/routing, здесь только компоновка и
// импорт файла, который касается сразу и режима, и правки текстом.
import { onMounted, ref } from 'vue'
import { useBulkEdit } from '../../composables/useBulkEdit'
import { downloadRules, parseRulesFile } from '../../lib/rulesFile'
import { ruleText } from '../../lib/rules'
import { useGeo } from '../../stores/geo'
import { useRouting } from '../../stores/routing'
import { notify, notifyError } from '../../stores/toasts'
import SectionHead from '../SectionHead.vue'
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
