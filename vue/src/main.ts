import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import './styles/globals.css'
import { installAuthLifecycle } from '@/lib/auth/lifecycle'

const app = createApp(App)

const pinia = createPinia()
app.use(pinia)
installAuthLifecycle(pinia, router)
app.use(router)

app.mount('#app')
