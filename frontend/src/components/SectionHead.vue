<template>
  <header class="head">
    <div class="head__text">
      <h2 class="head__title">{{ title }}</h2>
      <p v-if="note" class="head__note">{{ note }}</p>
    </div>
    <div v-if="$slots.default" class="head__actions"><slot /></div>
  </header>
</template>

<script setup lang="ts">
// Заголовок раздела: название, одна строка о том, что здесь, и действия
// раздела справа. Один вид на все разделы — чтобы сразу было видно, где
// кончается одно и начинается другое.
defineProps<{ title: string; note?: string }>()
</script>

<style scoped>
.head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  margin-bottom: 12px;
}

.head__text {
  flex: 1;
  min-width: 0;
}

.head__title {
  font-size: 15px;
  font-weight: 600;
  line-height: 1.3;
}

.head__note {
  margin-top: 2px;
  color: var(--muted);
  font-size: 12px;
}

.head__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px;
}
</style>
