<template>
  <form class="form" @submit.prevent="submit" @keydown.ctrl.enter.prevent="submit" @keydown.meta.enter.prevent="submit">
    <label class="field">
      <span>Название <span class="faint">— необязательно, иначе адрес сервера</span></span>
      <input v-model="name" type="text" class="input" autocomplete="off" />
    </label>
    <div class="field">
      <span class="row">
        <label class="grow" :for="areaId">Текст конфигурации</label>
        <button type="button" class="btn btn--ghost btn--sm" @click="file?.click()">
          <FolderOpen aria-hidden="true" />Открыть файл…
        </button>
      </span>
      <textarea
        :id="areaId"
        ref="text"
        v-model="content"
        class="input"
        :class="{ 'input--drop': dragging }"
        spellcheck="false"
        placeholder="Вставьте текст конфигурации, выданной сервером, или перетащите сюда файл"
        @dragover.prevent="dragging = true"
        @dragleave="dragging = false"
        @drop.prevent="onDrop"
      ></textarea>
      <span v-if="formats" class="faint note">Поддерживаются: {{ formats }}</span>
    </div>
    <div class="row actions">
      <span class="faint hint"><kbd>Ctrl</kbd>+<kbd>Enter</kbd></span>
      <span class="grow"></span>
      <button class="btn btn--ghost" type="button" @click="$emit('cancel')">Отмена</button>
      <button class="btn btn--primary" type="submit" :class="{ 'btn--busy': busy }" :disabled="!content.trim() || busy">
        <LoaderCircle v-if="busy" class="spin" aria-hidden="true" />
        {{ busy ? 'Сохранение…' : 'Сохранить' }}
      </button>
    </div>
    <input ref="file" type="file" hidden @change="onPick" />
  </form>
</template>

<script setup lang="ts">
// Форма подключения — одна и для добавления, и для правки: вставить текст
// или открыть файл (кнопкой или перетаскиванием).
import { FolderOpen, LoaderCircle } from '@lucide/vue'
import { onMounted, ref, useId } from 'vue'

const props = defineProps<{ initialName?: string; initialContent?: string; busy?: boolean; formats?: string }>()
const emit = defineEmits<{ save: [name: string, content: string]; cancel: [] }>()

const name = ref(props.initialName ?? '')
const content = ref(props.initialContent ?? '')
const dragging = ref(false)
const text = ref<HTMLTextAreaElement | null>(null)
const file = ref<HTMLInputElement | null>(null)
const areaId = useId()

function submit() {
  if (content.value.trim() && !props.busy) emit('save', name.value.trim(), content.value)
}

async function load(picked: File | undefined) {
  if (!picked) return
  content.value = await picked.text()
  // Имя файла — хорошее название по умолчанию: его обычно и дают серверу.
  if (!name.value.trim()) name.value = picked.name.replace(/\.[^.]+$/, '')
}

function onPick(e: Event) {
  const input = e.target as HTMLInputElement
  const picked = input.files?.[0]
  input.value = ''
  load(picked)
}

function onDrop(e: DragEvent) {
  dragging.value = false
  load(e.dataTransfer?.files?.[0])
}

onMounted(() => text.value?.focus())
</script>

<style scoped>
.form {
  display: grid;
  gap: 16px;
}

.input--drop {
  background: var(--surface-hover);
}

.note,
.hint {
  font-size: 12px;
}

@media (max-width: 640px) {
  .hint {
    display: none;
  }
}
</style>
