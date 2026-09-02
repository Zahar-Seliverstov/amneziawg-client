<template>
  <section class="section">
    <div v-if="!adding" class="section__head">
      <span class="hint">{{ configs.length ? `${configs.length} шт.` : '' }}</span>
      <button class="btn btn--quiet" @click="startAdd">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="3.1" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
        Добавить
      </button>
    </div>

    <TransitionGroup v-if="configs.length" tag="ul" name="list" class="list">
      <li
        v-for="cfg in configs"
        :key="cfg.id"
        class="card"
        :class="{ 'card--open': editingId === cfg.id }"
      >
        <div
          class="row row--tap"
          :class="{ 'row--selected': selectedId === cfg.id }"
          @click="$emit('select', cfg.id)"
        >
          <span class="mark">
            <svg v-if="selectedId === cfg.id" viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="3.3" stroke-linecap="round" stroke-linejoin="round">
              <path d="M20 6 9 17l-5-5" />
            </svg>
          </span>

          <span class="row__name">{{ cfg.name }}</span>

          <Transition name="fade">
            <span v-if="isConnected && status?.config_id === cfg.id" class="tag tag--on">
              активна
            </span>
          </Transition>

          <button
            class="icon-btn"
            :title="editingId === cfg.id ? 'Свернуть' : 'Изменить'"
            aria-label="Изменить"
            :disabled="inUse(cfg.id)"
            @click.stop="toggleEdit(cfg)"
          >
            <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.7" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 20h9" />
              <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z" />
            </svg>
          </button>

          <button
            class="icon-btn icon-btn--danger"
            title="Удалить"
            aria-label="Удалить"
            :disabled="inUse(cfg.id)"
            @click.stop="remove(cfg.id)"
          >
            <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.7" stroke-linecap="round" stroke-linejoin="round">
              <path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6" />
            </svg>
          </button>
        </div>

        <!-- Правка раскрывается прямо в строке: сразу видно, какой конфиг
             меняешь, и незачем искать форму где-то под списком -->
        <Transition name="expand">
          <div v-if="editingId === cfg.id" class="expand">
            <div class="card__body">
              <div class="field">
                <label>Название</label>
                <input v-model="editName" type="text" class="input" placeholder="Возьмём из адреса сервера" />
              </div>

              <div class="field">
                <label>Содержимое .conf файла</label>
                <textarea v-model="editContent" class="input input--mono"></textarea>
              </div>

              <div class="form__actions">
                <button class="btn" @click="cancelEdit">Отмена</button>
                <button class="btn btn--accent" @click="saveEdit">Сохранить</button>
              </div>
            </div>
          </div>
        </Transition>
      </li>
    </TransitionGroup>

    <p v-else class="muted">Нет сохранённых конфигураций</p>

    <!-- Добавление прямо на странице, без окна поверх -->
    <Transition name="expand">
      <div v-if="adding" class="expand">
        <div class="form">
          <h3 class="form__title">Новая конфигурация</h3>

          <div class="field">
            <label>Название <span class="field__note">необязательно</span></label>
            <input v-model="newName" type="text" class="input" placeholder="Возьмём из адреса сервера" />
          </div>

          <div class="field">
            <label>Содержимое .conf файла</label>
            <textarea
              v-model="newContent"
              class="input input--mono"
              placeholder="[Interface]&#10;PrivateKey = ...&#10;Address = 10.0.0.2/32&#10;&#10;[Peer]&#10;PublicKey = ...&#10;Endpoint = server:51820&#10;AllowedIPs = 0.0.0.0/0"
            ></textarea>
          </div>

          <div class="form__actions">
            <button class="btn" @click="cancelAdd">Отмена</button>
            <button class="btn btn--accent" @click="saveNew">Сохранить</button>
          </div>
        </div>
      </div>
    </Transition>
  </section>
</template>

<script setup lang="ts">
import type { AmneziaConfig, ConnectionStatus } from '~/composables/useApi'

// Список конфигураций с формами добавления и правки.
//
// Формы и их отправка живут здесь целиком: только так форма остаётся
// открытой, когда служба отвергла содержимое, — вставленный текст не должен
// пропадать вместе с ошибкой. Наверх уходит одно событие «список изменился»:
// сам список принадлежит странице, и перечитывает его она.
const props = defineProps<{
  configs: AmneziaConfig[]
  selectedId: string | null
  status: ConnectionStatus | null
}>()

const emit = defineEmits<{
  select: [id: string]
  changed: []
  notify: [text: string]
}>()

const api = useApi()

const adding = ref(false)
const newName = ref('')
const newContent = ref('')

const editingId = ref<string | null>(null)
const editName = ref('')
const editContent = ref('')

const isConnected = computed(() => props.status?.state === 'connected')

// Конфигурацию, на которой держится соединение, не даём ни менять, ни
// удалять: туннель уже поднят по её параметрам.
function inUse(id: string) {
  return props.status?.config_id === id && props.status?.state !== 'disconnected'
}

// Повторное нажатие на карандаш сворачивает форму — открывать её нечем,
// кроме той же кнопки, поэтому она и закрывает.
async function toggleEdit(cfg: AmneziaConfig) {
  if (editingId.value === cfg.id) {
    cancelEdit()
    return
  }

  // Текст .conf в списке не приходит — в нём приватный ключ, и запрашивается
  // он только здесь. Форму открываем, лишь получив текст: иначе пользователь
  // увидел бы пустое поле и решил, что от конфигурации ничего не осталось.
  try {
    const detail = await api.getConfig(cfg.id)
    cancelAdd()
    editingId.value = cfg.id
    editName.value = detail.name
    editContent.value = detail.raw_config
  } catch (e: any) {
    emit('notify', e.message)
  }
}

function cancelEdit() {
  editingId.value = null
  editName.value = ''
  editContent.value = ''
}

async function saveEdit() {
  const id = editingId.value
  if (!id) return

  if (!editContent.value) {
    emit('notify', 'Содержимое .conf файла не может быть пустым')
    return
  }

  try {
    // Пустое название служба заполнит сама — из адреса сервера.
    await api.updateConfig(id, editName.value.trim(), editContent.value)
    cancelEdit()
    emit('changed')
  } catch (e: any) {
    emit('notify', e.message)
  }
}

function startAdd() {
  cancelEdit()
  adding.value = true
}

function cancelAdd() {
  adding.value = false
  newName.value = ''
  newContent.value = ''
}

async function saveNew() {
  if (!newContent.value) {
    emit('notify', 'Вставьте содержимое .conf файла')
    return
  }

  try {
    await api.addConfig(newName.value.trim(), newContent.value)
    cancelAdd()
    emit('changed')
  } catch (e: any) {
    emit('notify', e.message)
  }
}

async function remove(id: string) {
  try {
    await api.deleteConfig(id)
    if (editingId.value === id) cancelEdit()
    emit('changed')
  } catch (e: any) {
    emit('notify', e.message)
  }
}
</script>
