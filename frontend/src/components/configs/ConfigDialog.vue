<template>
  <dialog ref="dialog" class="dialog" :aria-labelledby="titleId" @cancel.prevent="$emit('close')" @click.self="$emit('close')">
    <div class="dialog__body">
      <header class="row dialog__head">
        <h2 :id="titleId" class="grow">{{ title }}</h2>
        <button class="btn btn--ghost btn--icon btn--sm" type="button" aria-label="Закрыть" title="Закрыть · Esc" @click="$emit('close')">
          <X aria-hidden="true" />
        </button>
      </header>
      <ConfigForm
        :initial-name="initialName"
        :initial-content="initialContent"
        :busy="busy"
        :formats="formats"
        @save="(name, content) => $emit('save', name, content)"
        @cancel="$emit('close')"
      />
    </div>
  </dialog>
</template>

<script setup lang="ts">
// Окно добавления и правки подключения. Встроенный <dialog> даёт и
// затемнение, и удержание фокуса внутри, и Esc — без своего кода.
import { X } from '@lucide/vue'
import { onMounted, ref, useId } from 'vue'
import ConfigForm from './ConfigForm.vue'

defineProps<{ title: string; initialName?: string; initialContent?: string; busy: boolean; formats: string }>()
defineEmits<{ save: [name: string, content: string]; close: [] }>()

const dialog = ref<HTMLDialogElement | null>(null)
const titleId = useId()

onMounted(() => dialog.value?.showModal())
</script>

<style scoped>
.dialog {
  width: min(560px, calc(100vw - 24px));
  max-height: calc(100dvh - 24px);
  margin: auto;
  padding: 0;
  border: 1px solid var(--line);
  border-radius: 12px;
  background: var(--bg);
  color: var(--text);
  animation: fade var(--t);
}

.dialog::backdrop {
  background: var(--backdrop);
  animation: fade var(--t);
}

.dialog__body {
  padding: 20px 24px 24px;
}

.dialog__head {
  margin-bottom: 16px;
}

h2 {
  font-size: 16px;
  font-weight: 600;
}

@keyframes fade {
  from { opacity: 0; }
}

@media (max-width: 640px) {
  .dialog__body {
    padding: 16px;
  }
}
</style>
