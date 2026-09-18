<script lang="ts" setup>
import {computed, onBeforeUnmount, onMounted, ref} from 'vue'
import {Start, Status, Stop} from '../../wailsjs/go/main/App'
import {EventsOn} from '../../wailsjs/runtime'
import type {config, data} from '../../wailsjs/go/models'
import InfoBarStack from '../components/InfoBarStack.vue'
import NodeDialog from '../components/NodeDialog.vue'
import {useMessages} from '../composables/useMessages'

const messages = useMessages()

const status = ref<data.Status | null>(null)
// 只用来表示"正在启动/正在停止"这类过渡态；稳定态一律以后端返回的 running 为准，
// 这样 GUI 和终端版（gpp-tui）看到的状态永远一致。
const action = ref<'idle' | 'starting' | 'stopping'>('idle')
const dialogOpen = ref(false)

let timer: number | undefined
let cancelEvents: (() => void) | undefined

const running = computed(() => status.value?.running === true)
const gamePeer = computed(() => status.value?.game_peer ?? null)
const httpPeer = computed(() => status.value?.http_peer ?? null)
const noPeer = computed(() => !gamePeer.value && !httpPeer.value)

const stateText = computed(() => {
  if (action.value === 'starting') {
    return '正在启动…'
  }
  if (action.value === 'stopping') {
    return '正在停止…'
  }
  return running.value ? '加速中' : '未连接'
})

const stateTone = computed(() => {
  if (action.value !== 'idle') {
    return 'busy'
  }
  return running.value ? 'running' : 'idle'
})

// 说明"隧道由谁持有"：GUI 与 TUI 共用同一份状态，这里让用户看得见
const coreText = computed(() => {
  const current = status.value
  if (!current?.core_pid) {
    return ''
  }
  const who = current.core_kind === 'gui'
      ? '本窗口'
      : current.core_kind === 'tui' ? '终端版 gpp-tui' : current.core_kind
  return `核心：${who}（PID ${current.core_pid}）`
})

const configPath = computed(() => status.value?.config_path ?? '')

const stateSub = computed(() => {
  if (action.value === 'starting') {
    return '正在建立隧道，请稍候'
  }
  if (action.value === 'stopping') {
    return '正在断开隧道'
  }
  if (running.value) {
    return '隧道正在本机运行'
  }
  if (noPeer.value) {
    const count = status.value?.peer_count ?? 0
    return count > 0 ? `已导入 ${count} 个节点，还没有选择线路` : '还没有节点，先导入链接或订阅'
  }
  return '线路已选好，可以开始加速'
})

const mainButtonText = computed(() => {
  if (noPeer.value) {
    return '导入节点'
  }
  if (action.value === 'starting') {
    return '正在启动…'
  }
  if (action.value === 'stopping') {
    return '正在停止…'
  }
  return running.value ? '结束加速' : '开始加速'
})

const mainButtonDisabled = computed(() => action.value !== 'idle')

function humanRate(bytes: number): string {
  if (!bytes) {
    return '0 B/s'
  }
  if (bytes > 1024 * 1024) {
    return `${(bytes / 1024 / 1024).toFixed(2)} MB/s`
  }
  if (bytes > 1024) {
    return `${(bytes / 1024).toFixed(1)} KB/s`
  }
  return `${Math.round(bytes)} B/s`
}

function humanBytes(bytes: number): string {
  if (!bytes) {
    return '0 B'
  }
  if (bytes > 1024 * 1024 * 1024) {
    return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
  }
  if (bytes > 1024 * 1024) {
    return `${(bytes / 1024 / 1024).toFixed(2)} MB`
  }
  if (bytes > 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`
  }
  return `${Math.round(bytes)} B`
}

// toneOf 把延迟分档：<60ms 好、<100ms 一般、其余偏差；ping 为 0 表示没测出来
// （例如只监听 UDP 的 hysteria2），不要在界面上显示成 0ms。
function toneOf(peer: config.Peer | null): string {
  if (!peer || peer.ping <= 0) {
    return 'unknown'
  }
  if (peer.ping < 60) {
    return 'good'
  }
  if (peer.ping < 100) {
    return 'fair'
  }
  return 'poor'
}

function pingText(peer: config.Peer | null): string {
  if (!peer) {
    return '未选择'
  }
  return peer.ping > 0 ? `${peer.ping} ms` : '未测速'
}

async function refreshStatus() {
  try {
    const result = await Status()
    status.value = result
    // 后端的一次性提示（订阅更新失败、选中节点被自动切换等），取一次就清空
    if (result.warning) {
      messages.warning(result.warning)
    }
  } catch (error) {
    messages.error(`读取状态失败：${String(error)}`)
  }
}

async function start() {
  action.value = 'starting'
  try {
    const result = await Start()
    if (result !== 'ok' && result !== 'running') {
      messages.error(`加速失败：${result}`)
      return
    }
    // 先乐观置位，避免等下一次轮询（最多 1 秒）时按钮还写着"开始加速"
    if (status.value) {
      status.value.running = true
    }
    await refreshStatus()
  } finally {
    action.value = 'idle'
  }
}

async function stop() {
  action.value = 'stopping'
  try {
    const result = await Stop()
    if (result !== 'ok' && result !== 'not running') {
      messages.error(`停止失败：${result}`)
      return
    }
    if (status.value) {
      status.value.running = false
    }
    await refreshStatus()
  } finally {
    action.value = 'idle'
  }
}

// onMainButton：没有节点时按钮直接打开导入/选择弹窗，避免"灰按钮 + 不知道怎么导入"
function onMainButton() {
  if (noPeer.value) {
    openDialog()
    return
  }
  if (running.value) {
    void stop()
    return
  }
  void start()
}

function openDialog() {
  dialogOpen.value = true
}

onMounted(() => {
  void refreshStatus()
  timer = window.setInterval(() => void refreshStatus(), 1000)
  // 核心事件（另一端界面做的操作、订阅回退、自动切换节点等）立即提示，不用等轮询
  cancelEvents = EventsOn('gpp:event', (event: any) => {
    if (!event || !event.message) {
      return
    }
    if (event.type === 'warning') {
      messages.warning(event.message)
    } else if (event.type === 'started' || event.type === 'stopped') {
      messages.info(event.message)
    }
  })
})

onBeforeUnmount(() => {
  if (timer !== undefined) {
    window.clearInterval(timer)
  }
  cancelEvents?.()
})
</script>

<template>
  <main class="home">
    <div class="home__body">
      <!-- 对话框打开时把提示移到对话框里，否则会被遮罩挡住 -->
      <InfoBarStack v-if="!dialogOpen"/>

      <section class="card hero" :class="`hero--${stateTone}`">
        <div class="hero__row">
          <span class="hero__dot"></span>
          <div class="hero__texts">
            <p class="hero__state">{{ stateText }}</p>
            <p class="hero__sub">{{ stateSub }}</p>
          </div>
        </div>
        <div v-if="action !== 'idle'" class="progress" role="progressbar" aria-label="正在切换加速状态">
          <div class="progress__bar"></div>
        </div>
      </section>

      <section class="card">
        <div class="card__head">
          <span class="card__label">线路</span>
          <span class="card__hint">点击更改</span>
        </div>
        <button class="node" type="button" @click="openDialog()">
          <span class="node__kind">Game</span>
          <span class="node__name" :class="{'node__name--empty': !gamePeer}">
            {{ gamePeer ? gamePeer.name : '未选择' }}
          </span>
          <span class="node__meta" :class="`tone--${toneOf(gamePeer)}`">{{ pingText(gamePeer) }}</span>
        </button>
        <div class="divider"></div>
        <button class="node" type="button" @click="openDialog()">
          <span class="node__kind">Http</span>
          <span class="node__name" :class="{'node__name--empty': !httpPeer}">
            {{ httpPeer ? httpPeer.name : '未选择' }}
          </span>
          <span class="node__meta" :class="`tone--${toneOf(httpPeer)}`">{{ pingText(httpPeer) }}</span>
        </button>
      </section>

      <section v-if="running" class="traffic">
        <div class="tile">
          <span class="tile__label">下载</span>
          <span class="tile__value">{{ humanRate(status?.down_rate ?? 0) }}</span>
          <span class="tile__sub">累计 {{ humanBytes(status?.down ?? 0) }}</span>
        </div>
        <div class="tile">
          <span class="tile__label">上传</span>
          <span class="tile__value">{{ humanRate(status?.up_rate ?? 0) }}</span>
          <span class="tile__sub">累计 {{ humanBytes(status?.up ?? 0) }}</span>
        </div>
      </section>

      <div class="home__spacer"></div>

      <button
          class="btn btn--accent btn--block"
          type="button"
          :disabled="mainButtonDisabled"
          @click="onMainButton()"
      >
        {{ mainButtonText }}
      </button>

      <p v-if="coreText" class="home__foot" :title="coreText">{{ coreText }}</p>
      <p v-if="configPath" class="home__foot home__foot--dim" :title="configPath">配置：{{ configPath }}</p>
    </div>

    <NodeDialog
        :open="dialogOpen"
        :running="running"
        :game-peer="gamePeer?.name"
        :http-peer="httpPeer?.name"
        @close="dialogOpen = false"
        @saved="refreshStatus()"
    />
  </main>
</template>

<style scoped>
.home {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: 0;
}

.home__body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1 1 auto;
  min-height: 0;
  padding: 12px 16px 16px;
  overflow-y: auto;
}

.home__spacer {
  flex: 1 1 auto;
  min-height: 4px;
}

.hero {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.hero__row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.hero__dot {
  flex: 0 0 auto;
  width: 10px;
  height: 10px;
  margin-top: 5px;
  border-radius: 50%;
  background: var(--text-fill-tertiary);
}

.hero--running .hero__dot {
  background: var(--system-fill-success);
}

.hero--busy .hero__dot {
  background: var(--accent-fill-default);
  animation: dot-pulse 1.4s var(--ease-in-out) infinite;
}

@keyframes dot-pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.35;
  }
}

.hero__texts {
  flex: 1 1 auto;
  min-width: 0;
}

.hero__state {
  margin: 0;
  font: var(--font-body-strong);
  color: var(--text-fill-primary);
}

.hero__sub {
  margin: 2px 0 0;
  font: var(--font-caption);
  color: var(--text-fill-secondary);
  overflow-wrap: anywhere;
}

.progress {
  position: relative;
  height: 4px;
  border-radius: 2px;
  background: var(--progress-track);
  overflow: hidden;
}

.progress__bar {
  position: absolute;
  top: 0;
  left: 0;
  width: 40%;
  height: 100%;
  border-radius: 2px;
  background: var(--accent-fill-default);
  animation: progress-slide 1.5s var(--ease-out) infinite;
}

@keyframes progress-slide {
  from {
    transform: translateX(-100%);
  }
  to {
    transform: translateX(250%);
  }
}

.card__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 2px;
}

.card__label {
  font: var(--font-caption);
  color: var(--text-fill-secondary);
}

.card__hint {
  font: var(--font-caption);
  color: var(--text-fill-tertiary);
}

.node {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 34px;
  padding: 0 8px;
  margin: 0 -8px;
  border: 0;
  border-radius: var(--control-corner-radius);
  background: transparent;
  text-align: left;
  cursor: default;
}

.node:hover {
  background: var(--control-fill-secondary);
}

.node:active {
  background: var(--control-fill-tertiary);
}

.node:focus-visible {
  outline: var(--focus-outline);
  outline-offset: -1px;
}

.node__kind {
  flex: 0 0 auto;
  width: 38px;
  font: var(--font-caption);
  color: var(--text-fill-tertiary);
}

.node__name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font: var(--font-body);
  color: var(--text-fill-primary);
}

.node__name--empty {
  color: var(--text-fill-secondary);
}

.node__meta {
  flex: 0 0 auto;
  font: var(--font-caption);
}

.divider {
  height: 1px;
  margin: 2px 0;
  background: var(--divider-stroke);
}

.traffic {
  display: flex;
  gap: 8px;
}

.tile {
  flex: 1 1 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: 8px 12px;
  border: 1px solid var(--card-stroke-default);
  border-radius: var(--control-corner-radius);
  background: var(--card-background-fill-secondary);
}

.tile__label {
  font: var(--font-caption);
  color: var(--text-fill-secondary);
}

.tile__value {
  font: var(--font-body-strong);
  color: var(--text-fill-primary);
  font-variant-numeric: tabular-nums;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tile__sub {
  font: var(--font-caption);
  color: var(--text-fill-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home__foot {
  margin: 0;
  font: var(--font-caption);
  color: var(--text-fill-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  /* 配置路径用户经常要复制出去排查问题，这里放开选中 */
  user-select: text;
}

.home__foot--dim {
  color: var(--text-fill-tertiary);
}
</style>
