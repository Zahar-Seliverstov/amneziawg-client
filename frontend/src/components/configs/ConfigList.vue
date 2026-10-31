<template>
  <section>
    <SectionHead title="Подключения" note="Файлы .conf от сервера AmneziaWG">
      <button v-if="!adding" class="btn btn--sm" @click="startAdd">+ Добавить</button>
    </SectionHead>

    <ConfigForm v-if="adding" :busy="busy" @save="create" @cancel="adding = false" />

    <ul v-if="configs.length" class="list" role="radiogroup" aria-label="Подключения">
      <li v-for="cfg in configs" :key="cfg.id">
        <div
          class="cfg"
          :class="{ 'cfg--sel': selectedId === cfg.id }"
          role="radio"
          tabindex="0"
          :aria-checked="selectedId === cfg.id"
          @click="$emit('select', cfg.id)"
          @keydown.enter.space.prevent="$emit('select', cfg.id)"
        >
          <span class="radio" aria-hidden="true"></span>
          <span class="cfg__name mono ellipsis" :title="cfg.name">{{ cfg.name }}</span>
          <span v-if="liveId === cfg.id" class="badge">активно</span>
        </div>

        <div v-if="selectedId === cfg.id && editingId !== cfg.id" class="actions">
          <span v-if="inUse(cfg.id)" class="faint note">Используется — отключитесь, чтобы изменить или удалить</span>
          <template v-else>
            <button class="btn btn--ghost btn--sm" @click="startEdit(cfg)">Изменить</button>
            <button
              class="btn btn--ghost btn--sm"
              :class="{ 'btn--danger': confirmId === cfg.id }"
              @click="remove(cfg.id)"
            >{{ confirmId === cfg.id ? 'Точно удалить?' : 'Удалить' }}</button>
          </template>
        </div>

        <ConfigForm
          v-if="editingId === cfg.id"
          :initial-name="edit.name"
          :initial-content="edit.content"
          :busy="busy"
          @save="(name, content) => update(cfg.id, name, content)"
          @cancel="editingId = null"
        />
      </li>
    </ul>

    <p v-else-if="!adding" class="empty">
      Подключений пока нет. Добавьте файл .conf, выданный сервером AmneziaWG.
    </p>
  </section>
</template>

<script setup lang="ts">
// Список подключений: выбор, добавление, правка и удаление.
import { computed, reactive, ref } from 'vue'
import { api } from '../../api/client'
import type { AmneziaConfig, ConnectionStatus } from '../../api/types'
import { notifyError } from '../../stores/toasts'
import SectionHead from '../SectionHead.vue'
import ConfigForm from './ConfigForm.vue'

const props = defineProps<{
  configs: AmneziaConfig[]
  selectedId: string | null
  status: ConnectionStatus | null
}>()

const emit = defineEmits<{ select: [id: string]; changed: [] }>()

const adding = ref(false)
const busy = ref(false)
const editingId = ref<string | null>(null)
const edit = reactive({ name: '', content: '' })

const liveId = computed(() => (props.status?.state === 'connected' ? props.status.config_id : undefined))

// Конфигурацию, на которой держится соединение, менять нельзя: служба
// работает по ней прямо сейчас.
function inUse(id: string) {
  return props.status?.config_id === id && props.status.state !== 'disconnected'
}

async function run(action: () => Promise<unknown>) {
  busy.value = true
  try {
    await action()
    emit('changed')
    return true
  } catch (e) {
    notifyError(e)
    return false
  } finally {
    busy.value = false
  }
}

function startAdd() {
  editingId.value = null
  adding.value = true
}

async function create(name: string, content: string) {
  if (await run(() => api.addConfig(name, content))) adding.value = false
}

// Текст .conf запрашивается только для правки: в нём приватный ключ.
async function startEdit(cfg: AmneziaConfig) {
  try {
    const detail = await api.getConfig(cfg.id)
    edit.name = detail.name
    edit.content = detail.raw_config
    adding.value = false
    editingId.value = cfg.id
  } catch (e) {
    notifyError(e)
  }
}

async function update(id: string, name: string, content: string) {
  if (await run(() => api.updateConfig(id, name, content))) editingId.value = null
}

// Удаление в два нажатия: второе подтверждает, через 3 с ожидание сбрасывается.
const confirmId = ref<string | null>(null)
let confirmTimer: ReturnType<typeof setTimeout> | undefined

async function remove(id: string) {
  if (confirmId.value !== id) {
    confirmId.value = id
    clearTimeout(confirmTimer)
    confirmTimer = setTimeout(() => (confirmId.value = null), 3000)
    return
  }
  confirmId.value = null
  await run(() => api.deleteConfig(id))
}
</script>

<style scoped>
.list {
  border: 1px solid var(--line);
  border-radius: 4px;
}

.list > li + li {
  border-top: 1px solid var(--line);
}

.cfg {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 36px;
  padding: 0 10px;
  cursor: pointer;
}

.cfg:hover {
  background: var(--raise);
}

.cfg--sel {
  background: var(--sel);
}

.radio {
  width: 12px;
  height: 12px;
  flex: none;
  border: 1px solid var(--line-strong);
  border-radius: 50%;
}

.cfg--sel .radio {
  border: 4px solid var(--accent);
}

.cfg__name {
  flex: 1;
}

.badge {
  padding: 0 6px;
  border: 1px solid var(--ok);
  border-radius: 3px;
  color: var(--ok);
  font-size: 11px;
  line-height: 18px;
}

.actions {
  display: flex;
  align-items: center;
  gap: 4px;
  min-height: 28px;
  padding: 0 10px 8px 32px;
  background: var(--sel);
}

.note {
  font-size: 12px;
}

li > :deep(.form) {
  padding: 10px;
}
</style>
