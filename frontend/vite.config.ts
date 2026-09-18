import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'

// 界面已经改成手写的 Fluent 控件（见 src/styles/fluent.css），
// 不再需要 naive-ui 的按需引入/自动导入插件。
export default defineConfig({
    plugins: [
        vue(),
    ]
})
