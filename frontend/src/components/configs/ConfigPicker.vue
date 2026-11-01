<template>
  <div ref="root" class="picker">
    <button
      v-if="configs.length"
      ref="trigger"
      type="button"
      class="picker__btn"
      :aria-expanded="open"
      aria-haspopup="dialog"
      :disabled="locked"
      :title="locked ? 'Дождитесь отключения' : 'Выбрать подключение'"
      @click="open = !open"
    >
      <span class="ellipsis">{{ current }}</span>
      <ChevronDown class="picker__chev" aria-hidden="true" />
    </button>
    <button v-else type="button" class="picker__btn" @click="startAdd">
      <Plus aria-hidden="true" /><span>Добавить подключение</span>
    </button>

    <Transition name="pop">
      <div v-if="open" class="pop menu" role="dialog" aria-label="Подключения" @keydown="onMenuKey">
        <ul class="menu__list">
          <li v-for="cfg in configs" :key="cfg.id" class="item" :class="{ 'item--sel': cfg.id === selectedId }">
            <button
              type="button"
              class="item__pick"
              :aria-current="cfg.id === selectedId"
              data-pick
              @click="choose(cfg.id)"
            >
              <Check class="item__check" aria-hidden="true" />
              <span class="grow">
                <span class="item__name ellipsis" :title="cfg.name">{{ cfg.name }}</span>
                <span class="item__meta">{{ metaOf(cfg) }}</span>
              </span>
            </button>
            <template v-if="!inUse(cfg.id)">
              <button type="button" class="btn btn--ghost btn--icon btn--sm" :aria-label="`Изменить ${cfg.name}`" title="Изменить" @click="startEdit(cfg)">
                <Pencil aria-hidden="true" />
              </button>
              <button
                type="button"
                class="btn btn--ghost btn--icon btn--sm"
                :class="{ confirm: confirmId === cfg.id }"
                :aria-label="confirmId === cfg.id ? `Точно удалить ${cfg.name}?` : `Удалить ${cfg.name}`"
                :title="confirmId === cfg.id ? 'Нажмите ещё раз, чтобы удалить' : 'Удалить'"
                @click="remove(cfg.id)"
              >
                <Trash2 aria-hidden="true" />
                <span v-if="confirmId === cfg.id">Удалить?</span>
              </button>
            </template>
            <span v-else class="faint lock" title="Используется — отключитесь, чтобы изменить или удалить">
              <Lock aria-hidden="true" />
            </span>
          </li>
        </ul>
        <button type="button" class="menu__add" @click="startAdd">
          <Plus aria-hidden="true" />Добавить подключение
        </button>
      </div>
    </Transition>

    <ConfigDialog
      v-if="dialog"
      :title="dialog.id ? 'Изменить подключение' : 'Новое подключение'"
      :initial-name="dialog.name"
      :initial-content="dialog.content"
      :busy="busy"
      :formats="formats"
      @save="save"
      @close="dialog = null"
    />
  </div>
</template>

<script setup lang="ts">
// Выбор подключения — компактный, в карточке соединения: подключений
// обычно одно-два, и отдельная колонка под них отнимала пол-экрана на
// телефоне. Здесь же добавление, правка и удаление.
import { Check, ChevronDown, Lock, Pencil, Plus, Trash2 } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '../../api/client'
import type { ConnectionStatus, Protocol, SavedConfig } from '../../api/types'
import { notify, notifyError } from '../../stores/toasts'
import ConfigDialog from './ConfigDialog.vue'

const props = defineProps<{
  configs: SavedConfig[]
  protocols: Protocol[]
  selectedId: string | null
  activeName: string
  status: ConnectionStatus | null
}>()

const emit = defineEmits<{ select: [id: string]; changed: [] }>()

const open = ref(false)
const busy = ref(false)
const dialog = ref<{ id: string | null; name: string; content: string } | null>(null)
const root = ref<HTMLElement | null>(null)
const trigger = ref<HTMLButtonElement | null>(null)

// Какие форматы принимаются — по списку протоколов службы, а не зашито.
const formats = computed(() => props.protocols.map(p => p.title).join(', '))

// Пока соединение живо, показываем то, на чём оно держится.
const current = computed(() => {
  const live = props.status && props.status.state !== 'disconnected' && props.status.state !== 'error'
  if (live && props.activeName) return props.activeName
  return props.configs.find(c => c.id === props.selectedId)?.name ?? 'Выберите подключение'
})

// Во время разбора соединения выбор ждёт: там уже снимаются маршруты.
const locked = computed(() => props.status?.state === 'disconnecting')

const liveId = computed(() => (props.status?.state === 'connected' ? props.status.config_id : undefined))

// Под именем — формат (если их служба знает несколько) и «в работе».
function metaOf(cfg: SavedConfig) {
  const parts: string[] = []
  if (props.protocols.length > 1) parts.push(props.protocols.find(p => p.id === cfg.protocol)?.title ?? '')
  if (liveId.value === cfg.id) parts.push('в работе')
  return parts.filter(Boolean).join(' · ')
}

// Конфигурацию, на которой держится соединение, менять нельзя: служба
// работает по ней прямо сейчас.
function inUse(id: string) {
  return props.status?.config_id === id && props.status.state !== 'disconnected'
}

function close(refocus = true) {
  open.value = false
  if (refocus) nextTick(() => trigger.value?.focus())
}

function choose(id: string) {
  emit('select', id)
  close()
}

// ── Окошко: фокус внутрь при открытии, закрытие кликом мимо и Esc ──────
watch(open, isOpen => {
  if (isOpen) {
    nextTick(() => {
      const items = picks()
      ;(items.find(b => b.getAttribute('aria-current') === 'true') ?? items[0])?.focus()
    })
    document.addEventListener('pointerdown', onOutside)
  } else {
    document.removeEventListener('pointerdown', onOutside)
    confirmId.value = null
  }
})

// Окно формы закрылось — фокус обратно на выбор, а не в начало страницы.
watch(dialog, (now, was) => {
  if (was && !now) nextTick(() => trigger.value?.focus())
})

function onOutside(e: PointerEvent) {
  if (!root.value?.contains(e.target as Node)) close(false)
}

function picks() {
  return [...(root.value?.querySelectorAll<HTMLButtonElement>('[data-pick]') ?? [])]
}

function onMenuKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    // Esc закрывает только окошко, а не уводит со страницы.
    e.stopPropagation()
    close()
    return
  }
  if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
  e.preventDefault()
  const items = picks()
  const at = items.indexOf(document.activeElement as HTMLButtonElement)
  const step = e.key === 'ArrowDown' ? 1 : -1
  items[(at + step + items.length) % items.length]?.focus()
}

onBeforeUnmount(() => document.removeEventListener('pointerdown', onOutside))

// ── Добавление, правка, удаление ─────────────────────────────────────
function startAdd() {
  open.value = false
  dialog.value = { id: null, name: '', content: '' }
}

// Текст конфигурации запрашивается только для правки: в нём приватный ключ.
async function startEdit(cfg: SavedConfig) {
  try {
    const detail = await api.getConfig(cfg.id)
    open.value = false
    dialog.value = { id: cfg.id, name: detail.name, content: detail.raw_config }
  } catch (e) {
    notifyError(e)
  }
}

async function save(name: string, content: string) {
  const id = dialog.value?.id
  busy.value = true
  try {
    if (id) await api.updateConfig(id, name, content)
    else await api.addConfig(name, content)
    dialog.value = null
    notify(id ? 'Подключение сохранено' : 'Подключение добавлено', 'ok')
    emit('changed')
  } catch (e) {
    notifyError(e)
  } finally {
    busy.value = false
  }
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
  try {
    await api.deleteConfig(id)
    emit('changed')
  } catch (e) {
    notifyError(e)
  }
}
</script>

<style scoped>
.picker {
  position: relative;
  min-width: 0;
  max-width: calc(100% + 12px);
  /* Поля кнопки нужны только под фон при наведении: в строке имя должно
     стоять вровень с текстом слева и на обычном расстоянии от «·». */
  margin: 0 -4px 0 -8px;
}

/* Имя подключения с шевроном — это и есть кнопка выбора: без рамки,
   фон появляется только при наведении. */
.picker__btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  height: 28px;
  padding: 0 6px 0 8px;
  border-radius: var(--r);
  color: var(--muted);
  transition: background-color var(--t), color var(--t);
}

.picker__btn > span {
  min-width: 0;
}

.picker__btn:hover:not(:disabled),
.picker__btn[aria-expanded='true'] {
  background: var(--surface);
  color: var(--text);
}

.picker__btn:disabled {
  opacity: 0.5;
  cursor: default;
}

.picker__btn svg {
  width: 14px;
  height: 14px;
}

.menu {
  top: calc(100% + 6px);
  left: 0;
  width: min(320px, calc(100vw - 32px));
  padding: 4px;
}

.menu__list {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 1px;
}

.item {
  display: flex;
  align-items: center;
  gap: 2px;
  padding-right: 4px;
  border-radius: 6px;
  transition: background-color var(--t);
}

.item:hover {
  background: var(--surface);
}

.item__pick {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
  padding: 6px 8px;
  border-radius: 6px;
  text-align: left;
}

.item__check {
  color: var(--text);
  visibility: hidden;
}

.item--sel .item__check {
  visibility: visible;
}

.item__name {
  display: block;
}

.item__meta {
  display: block;
  color: var(--faint);
  font-size: 12px;
}

.item__meta:empty {
  display: none;
}

.lock {
  display: grid;
  place-items: center;
  width: 28px;
  color: var(--faint);
}

.lock svg {
  width: 14px;
  height: 14px;
}

.confirm {
  width: auto;
  padding: 0 8px;
  color: var(--error);
}

.menu__add {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  margin-top: 4px;
  padding: 8px;
  border-top: 1px solid var(--line);
  color: var(--muted);
  transition: color var(--t);
}

.menu__add:hover {
  color: var(--text);
}
</style>
