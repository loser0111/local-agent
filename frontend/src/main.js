import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
// tokens.css 必须在 global.scss 之前引入：它定义 :root 上的主题 CSS 变量，
// global.scss（及所有组件的 scss）通过 variables.scss 的映射层消费这些变量。
import './assets/styles/tokens.css'
import './assets/styles/global.scss'
import { useUiStore } from '@/stores/ui'
import { useSettingStore } from '@/stores/setting'
import { installImeGuard } from '@/utils/imeGuard'

const app = createApp(App)
app.use(createPinia())
app.use(router)

// 输入法守卫：记录 compositionend 时间戳，供各键盘处理入口识别「确认候选词的那次按键」。
// 必须尽早安装——macOS WebKit 的 compositionend 先于 keydown，晚装就可能漏掉第一次。
installImeGuard()

// 应用已保存的主题与字号（index.html 里的内联脚本已抢先应用过一次，
// 这里做一次权威同步，并挂上「跟随系统」的监听）
useSettingStore().applyAppearance()

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
