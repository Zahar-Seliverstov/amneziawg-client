<template>
  <form class="form" @submit.prevent="submit">
    <label class="field">
      <span>Название <span class="faint">— необязательно, иначе адрес сервера</span></span>
      <input v-model="name" type="text" class="input" />
    </label>
    <label class="field">
      <span class="row">
        <span class="grow">Файл конфигурации .conf</span>
        <button type="button" class="btn btn--ghost btn--sm" @click="file?.click()">Открыть файл…</button>
      </span>
      <textarea
        ref="text"
        v-model="content"
        class="input"
        spellcheck="false"
        placeholder="[Interface]&#10;PrivateKey = …&#10;Address = 10.0.0.2/32&#10;&#10;[Peer]&#10;PublicKey = …&#10;Endpoint = host:51820"
        @keydown.ctrl.enter.prevent="submit"
      ></textarea>
    </label>
    <div class="row">
      <button class="btn btn--primary" type="submit" :disabled="!content.trim() || busy">Сохранить</button>
      <button class="btn btn--ghost" type="button" @click="$emit('cancel')">Отмена</button>
      <span class="faint hint">Ctrl+Enter</span>
    </div>
    <input ref="file" type="file" accept=".conf,text/plain" hidden @change="onFile" />
  </form>
</template>

<script setup lang="ts">
// Форма подключения — одна и для добавления, и для правки.
import { onMounted, ref } from 'vue'

const props = defineProps<{ initialName?: string; initialContent?: string; busy?: boolean }>()
const emit = defineEmits<{ save: [name: string, content: string]; cancel: [] }>()

const name = ref(props.initialName ?? '')
const content = ref(props.initialContent ?? '')
const text = ref<HTMLTextAreaElement | null>(null)
const file = ref<HTMLInputElement | null>(null)

function submit() {
  if (content.value.trim()) emit('save', name.value.trim(), content.value)
}

async function onFile(e: Event) {
  const input = e.target as HTMLInputElement
  const picked = input.files?.[0]
  input.value = ''
  if (!picked) return
  content.value = await picked.text()
  if (!name.value.trim()) name.value = picked.name.replace(/\.conf$/i, '')
}

onMounted(() => text.value?.focus())
</script>

<style scoped>
.form {
  display: grid;
  gap: 10px;
  padding: 10px 0 14px;
}

.hint {
  font-size: 12px;
}
</style>
