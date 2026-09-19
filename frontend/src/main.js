import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import './assets/styles/global.scss'
import { useUiStore } from '@/stores/ui'

const app = createApp(App)
app.use(createPinia())
app.use(router)

// 把原生 alert 接管成应用内提示：
// 原生对话框在部分 webview 上会被直接吞掉（点了没反应、也不报错），
// 而全项目有十余处 alert 用来报"删除失败""保存失败"这类关键信息，不能依赖宿主。
// 提示层未就绪时退回原生实现，绝不静默丢消息。
const nativeAlert = window.alert?.bind(window)
window.alert = (message) => {
  try {
    useUiStore().notify(String(message ?? ''), 'error')
  } catch {
    nativeAlert?.(message)
  }
}

app.mount('#app')
