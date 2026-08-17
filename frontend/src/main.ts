import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import { initializeAuth } from './stores/auth'
import { initializeLocale } from './stores/i18n'
import '../css/style.css'

async function bootstrap(): Promise<void> {
  initializeLocale()
  await initializeAuth()
  createApp(App).use(router).mount('#app')
}

void bootstrap()
