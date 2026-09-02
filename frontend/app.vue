<template>
  <!-- Пока служба поднимается, окно уже открыто и показывает ожидание:
       пользователь в это время вводит пароль в диалоге polkit, и пустое
       окно выглядело бы как зависшее приложение. -->
  <div v-if="boot.state !== 'ready'" class="boot" :class="{ failed: boot.state === 'failed' }">
    <div class="box">
      <span class="logo" aria-hidden="true" />
      <h1>{{ boot.state === 'failed' ? 'Не удалось запустить службу' : 'Запуск службы AmneziaWG…' }}</h1>
      <p>
        {{ boot.state === 'failed'
          ? 'Окно можно закрыть и попробовать снова.'
          : 'Подтвердите права администратора — они нужны для TUN-интерфейса и маршрутов.' }}
      </p>
      <pre v-if="boot.state === 'failed'">{{ boot.message }}</pre>
    </div>
  </div>

  <NuxtPage v-else />
</template>

<script setup lang="ts">
import { invoke } from '@tauri-apps/api/core'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'

// Ровно то, что отдаёт commands::BootState в оболочке.
interface Boot {
  state: 'starting' | 'ready' | 'failed'
  message?: string
}

const boot = ref<Boot>({ state: 'starting' })

let unlisten: UnlistenFn | null = null

onMounted(async () => {
  // Подписываемся до опроса: между ответом на запрос и подпиской иначе была
  // бы щель, в которую проваливается событие о готовности.
  unlisten = await listen<Boot>('backend:boot', (event) => {
    boot.value = event.payload
  })

  try {
    boot.value = await invoke<Boot>('backend_state')
  } catch (e) {
    boot.value = { state: 'failed', message: String(e) }
  }
})

onUnmounted(() => {
  unlisten?.()
  unlisten = null
})
</script>

<style scoped>
.boot {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #0f1115;
  color: #e6e8ee;
  font: 14px/1.5 system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif;
  user-select: none;
}

.box {
  text-align: center;
  max-width: 32rem;
  padding: 2rem;
}

/* Пульсация вместо спиннера: логотип сам показывает, что идёт запуск. */
.logo {
  display: block;
  margin: 0 auto 1.25rem;
  width: 72px;
  height: 72px;
  background-color: #fff;
  mask: url('/logo-mono.svg') no-repeat center / contain;
  -webkit-mask: url('/logo-mono.svg') no-repeat center / contain;
  animation: breathe 1.8s ease-in-out infinite;
}

.failed .logo {
  animation: none;
  opacity: 0.45;
}

@keyframes breathe {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.72; transform: scale(0.94); }
}

@media (prefers-reduced-motion: reduce) {
  .logo { animation: none; }
}

h1 {
  font-size: 1rem;
  font-weight: 600;
  margin: 0 0 0.35rem;
}

p {
  margin: 0;
  color: #9aa3b5;
}

pre {
  margin: 1rem 0 0;
  padding: 0.75rem;
  text-align: left;
  white-space: pre-wrap;
  background: #171a21;
  border: 1px solid #262b36;
  border-radius: 6px;
  color: #ff8f8f;
  font-size: 12px;
}
</style>
