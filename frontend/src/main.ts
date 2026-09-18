// 全局设计令牌必须先于组件样式加载，所以放在最前面
import './styles/fluent.css'
import {createApp} from 'vue'
import App from './App.vue'

// 原生应用不该弹出 Chromium 的右键菜单；输入框里的粘贴菜单要留着，
// 否则用户没法把导入链接粘进来。
window.addEventListener('contextmenu', event => {
  const target = event.target as HTMLElement | null
  if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA')) {
    return
  }
  event.preventDefault()
})

createApp(App).mount('#app')
