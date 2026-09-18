<script lang="ts" setup>
import {computed, nextTick, ref, watch} from 'vue'
import {Add, List, PingAll, SetPeer} from '../../wailsjs/go/main/App'
import type {config} from '../../wailsjs/go/models'
import FluentComboBox from './FluentComboBox.vue'
import InfoBarStack from './InfoBarStack.vue'
import {useMessages} from '../composables/useMessages'
import type {ComboOption} from '../types'

const props = defineProps<{
  open: boolean
  // 加速中改节点不会热生效，需要提示用户先停止
  running: boolean
  gamePeer?: string
  httpPeer?: string
}>()

const emit = defineEmits<{(e: 'close'): void; (e: 'saved'): void}>()

const messages = useMessages()

const dialogRef = ref<HTMLDivElement | null>(null)
const peers = ref<config.Peer[]>([])
const gameValue = ref<string>()
const httpValue = ref<string>()
const newUrl = ref('')
const loading = ref(false)
const pinging = ref(false)

// 延迟分档沿用旧界面：<60ms 好、<100ms 一般、其余偏差；ping 为 0 表示没测出来
// （例如只监听 UDP 的 hysteria2），显示成"未测速"而不是 0ms。
function peerOption(peer: config.Peer): ComboOption {
  const ping = peer.ping
  return {
    value: peer.name,
    label: peer.name,
    meta: ping > 0 ? `${ping} ms` : '未测速',
    tone: ping > 0 ? (ping < 60 ? 'good' : ping < 100 ? 'fair' : 'poor') : 'unknown',
  }
}

// 节点命名约定：以 http 开头的是 HTTP 分流节点，以 game 开头的是游戏节点，
// 这两条过滤规则和旧界面保持一致，不要改。
const gameOptions = computed<ComboOption[]>(() =>
    peers.value.filter(peer => !peer.name.startsWith('http')).map(peerOption))

const httpOptions = computed<ComboOption[]>(() =>
    peers.value.filter(peer => !peer.name.startsWith('game')).map(peerOption))

async function loadPeers() {
  loading.value = true
  try {
    peers.value = await List()
    // 选中的节点可能已经被删掉，清掉不存在的选择，避免提交一个失效的名字
    if (gameValue.value !== undefined && !gameOptions.value.some(item => item.value === gameValue.value)) {
      gameValue.value = undefined
    }
    if (httpValue.value !== undefined && !httpOptions.value.some(item => item.value === httpValue.value)) {
      httpValue.value = undefined
    }
  } finally {
    loading.value = false
  }
}

function close() {
  emit('close')
}

async function importUrl(url: string) {
  const result = await Add(url)
  if (result !== 'ok') {
    messages.error(result)
    return
  }
  messages.success('导入连接成功')
  newUrl.value = ''
  // 导入后立刻刷新列表，不用关掉弹窗再打开才能看到新节点
  await loadPeers()
  emit('saved')
}

async function applyPeer(game: string, http: string) {
  const result = await SetPeer(game, http)
  if (result !== 'ok') {
    messages.error(result)
    return
  }
  messages.success('设置节点成功')
  if (props.running) {
    // 加速中的实例不会热切换节点，必须重启才生效，这里明确告诉用户
    messages.warning('当前正在加速：请先「结束加速」再重新开始，新节点才会生效')
  }
  emit('saved')
}

function submit() {
  const url = newUrl.value.trim()
  const picked = gameValue.value !== undefined || httpValue.value !== undefined

  if (url && picked) {
    messages.error('只能选择一种方式')
    return
  }
  if (!url && !picked) {
    messages.error('请选择节点，或在下方粘贴导入链接')
    return
  }
  if (url) {
    void importUrl(url)
    return
  }
  if (gameValue.value === undefined) {
    messages.error('请选择 Game 节点')
    return
  }
  if (httpValue.value === undefined) {
    messages.error('请选择 Http 节点')
    return
  }
  void applyPeer(gameValue.value, httpValue.value)
}

async function reping() {
  pinging.value = true
  try {
    await PingAll()
    await loadPeers()
  } finally {
    pinging.value = false
  }
}

watch(() => props.open, opened => {
  if (!opened) {
    return
  }
  gameValue.value = props.gamePeer
  httpValue.value = props.httpPeer
  newUrl.value = ''
  void loadPeers()
  // 打开后把焦点收进对话框，这样 Esc 能直接生效
  void nextTick(() => dialogRef.value?.focus())
})
</script>

<template>
  <div v-if="open" class="layer">
    <div class="layer__smoke"></div>
    <div
        ref="dialogRef"
        class="dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="node-dialog-title"
        tabindex="-1"
        @keydown.esc="close"
    >
      <h2 id="node-dialog-title" class="dialog__title">节点与订阅</h2>
      <div class="dialog__body">
        <InfoBarStack/>

        <div class="field">
          <span class="field__label">Game 节点</span>
          <FluentComboBox
              v-model="gameValue"
              :options="gameOptions"
              placeholder="选择游戏线路"
              empty-text="还没有节点，请在下方粘贴导入链接"
          />
        </div>

        <div class="field">
          <span class="field__label">Http 节点</span>
          <FluentComboBox
              v-model="httpValue"
              :options="httpOptions"
              placeholder="选择分流线路"
              empty-text="还没有节点，请在下方粘贴导入链接"
          />
        </div>

        <div class="or"><span class="or__text">或</span></div>

        <div class="field">
          <span class="field__label">导入新连接</span>
          <textarea
              v-model="newUrl"
              class="textbox textbox--multiline"
              rows="3"
              placeholder="粘贴服务端生成的导入链接或订阅地址"
              spellcheck="false"
          ></textarea>
        </div>
      </div>

      <div class="dialog__footer">
        <button
            class="btn btn--subtle"
            type="button"
            :disabled="pinging || loading || running"
            :title="running ? '加速中不测速，避免干扰游戏流量；请先结束加速' : '重新测量各节点延迟'"
            @click="reping"
        >
          {{ pinging ? '测速中…' : (running ? '加速中不测速' : '重新测速') }}
        </button>
        <span class="dialog__spacer"></span>
        <button class="btn" type="button" @click="close">取消</button>
        <button class="btn btn--accent" type="button" @click="submit">保存</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.layer {
  position: fixed;
  inset: 0;
  z-index: 1000;
  /* 必须用 flex 而不是 grid：grid 的隐式行高会被内容撑开，此时子项的
     max-height:100% 是按"被撑开的行"算的，等于没限制，对话框会比窗口还高、
     顶部被裁掉；flex 容器高度是确定的，百分比 max-height 才真正生效。 */
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 12px;
}

.layer__smoke {
  position: absolute;
  inset: 0;
  background: var(--smoke-fill);
}

.dialog {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 100%;
  max-height: 100%;
  padding: 20px 20px 16px;
  border: 1px solid var(--surface-stroke-flyout);
  border-radius: var(--overlay-corner-radius);
  background: var(--solid-background-fill-base);
  box-shadow: var(--dialog-shadow);
  outline: none;
  animation: dialog-in 167ms var(--ease-out);
}

@keyframes dialog-in {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
}

.dialog__title {
  flex: 0 0 auto;
  margin: 0 0 12px;
  font: var(--font-subtitle);
  color: var(--text-fill-primary);
}

.dialog__body {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.field__label {
  font: var(--font-caption);
  color: var(--text-fill-secondary);
}

.or {
  display: flex;
  align-items: center;
  gap: 8px;
}

.or::before,
.or::after {
  content: '';
  flex: 1 1 auto;
  height: 1px;
  background: var(--divider-stroke);
}

.or__text {
  flex: 0 0 auto;
  font: var(--font-caption);
  color: var(--text-fill-tertiary);
}

.dialog__footer {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 16px;
}

.dialog__spacer {
  flex: 1 1 auto;
}

/* 窗口只有 360px 宽，对话框内宽约 296px，WinUI 默认的 120px 按钮最小宽度
   会把"重新测速/取消/保存"三个按钮挤出行外，这里按内容宽度收缩。 */
.dialog__footer .btn {
  min-width: 0;
}
</style>
