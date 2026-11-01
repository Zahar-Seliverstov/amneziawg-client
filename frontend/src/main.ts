import '@fontsource-variable/inter'
import { createApp } from 'vue'
import App from './App.vue'
import { initTheme } from './composables/useTheme'
import './style.css'

initTheme()
createApp(App).mount('#app')
