<script lang="ts" setup>
import TitleBar from './components/TitleBar.vue'
import Index from './views/Index.vue'
import {usesCustomChrome} from './composables/usePlatform'
</script>

<template>
  <!-- Windows 上是无边框窗口，标题栏由前端绘制（见 main.go / window_windows.go）；
       其他平台保留系统边框，这里就不再画标题栏，避免出现两条标题栏。 -->
  <div class="app">
    <TitleBar v-if="usesCustomChrome" title="gpp"/>
    <Index/>
  </div>
</template>

<style scoped>
.app {
  display: flex;
  flex-direction: column;
  height: 100vh;
  overflow: hidden;
}
</style>
