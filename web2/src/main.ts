import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import { i18n } from './lib/i18n'
import { useUiStore } from './stores/ui'
import './styles/index.css'

const app = createApp(App)
app.use(createPinia()) // installing pinia also marks it active, so the store call below works
app.use(router)
app.use(i18n)
// Re-assert the persisted theme through the store (index.html already set it
// pre-mount to avoid FOUC). From here the store is the single source of truth.
useUiStore().applyTheme()
app.mount('#app')
