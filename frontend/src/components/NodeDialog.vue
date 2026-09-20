<script lang="ts" setup>
import {computed, nextTick, ref, watch} from 'vue'
import {Add, Del, List, PingAll, SetPeer, Status} from '../../wailsjs/go/main/App'
import type {config} from '../../wailsjs/go/models'
import FluentComboBox from './FluentComboBox.vue'
import InfoBarStack from './InfoBarStack.vue'
import {useMessages} from '../composables/useMessages'
import type {ComboOption} from '../types'
import {canDeletePeer, decideNodeDialogAction, shouldConfirmHTTPDirect} from './nodeDialogLogic'

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
const importing = ref(false)
const deleting = ref<string>()

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

async function importUrl() {
  const action = decideNodeDialogAction(newUrl.value, gameValue.value, httpValue.value)
  if (action.kind !== 'import') {
    messages.error(action.kind === 'error' ? action.message : '请输入导入链接')
    return
  }
  importing.value = true
  try {
    const result = await Add(action.url)
    if (result !== 'ok') {
      messages.error(result)
      return
    }
    messages.success('导入连接成功')
    newUrl.value = ''
    // 导入后立刻刷新列表，不用关掉弹窗再打开才能看到新节点
    await loadPeers()
    emit('saved')
  } catch (error) {
    messages.error(`导入失败：${String(error)}`)
  } finally {
    importing.value = false
  }
}

async function applyPeer(game: string, http: string) {
  if (shouldConfirmHTTPDirect(game, http) && !window.confirm(
      'Http 选择“直连”后，境外网页将不经过代理，在受限网络中可能无法打开。\n\n仍要保存吗？')) {
    return
  }
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
  const action = decideNodeDialogAction('', gameValue.value, httpValue.value)
  if (action.kind === 'error') {
    messages.error(action.message)
    return
  }
  if (action.kind === 'select') {
    void applyPeer(action.game, action.http)
  }
}

async function deletePeer(peer: config.Peer) {
  if (!canDeletePeer(peer)) {
    messages.warning('“直连”是内置路由节点，不能删除')
    return
  }
  if (!window.confirm(`确定删除节点“${peer.name}”吗？`)) {
    return
  }
  deleting.value = peer.name
  try {
    const result = await Del(peer.name)
    if (result !== 'ok') {
      messages.error(result)
      return
    }
    await loadPeers()
    // 删除当前线路时后端会自动回退；从核心读取最终选择，避免界面留下空值或猜错回退节点。
    const current = await Status()
    gameValue.value = current.game_peer?.name
    httpValue.value = current.http_peer?.name
    if (current.warning) {
      messages.warning(current.warning)
    } else {
      messages.success(`已删除节点“${peer.name}”`)
    }
    emit('saved')
  } catch (error) {
    messages.error(`删除失败：${String(error)}`)
  } finally {
    deleting.value = undefined
  }
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
          <span
              v-if="shouldConfirmHTTPDirect(gameValue, httpValue)"
              class="field__help field__help--warning"
          >直连不会代理境外网页，部分网站可能无法打开。</span>
        </div>

        <div class="field">
          <span class="field__label">已导入节点</span>
          <div class="peer-list">
            <div v-for="peer in peers" :key="peer.name" class="peer-row">
              <span class="peer-row__name" :title="peer.name">{{ peer.name }}</span>
              <span class="peer-row__protocol">{{ peer.protocol === 'direct' ? '内置' : peer.protocol }}</span>
              <button
                  class="btn btn--subtle peer-row__delete"
                  type="button"
                  :disabled="!canDeletePeer(peer) || deleting !== undefined || loading"
                  :title="canDeletePeer(peer) ? `删除 ${peer.name}` : '内置直连节点不能删除'"
                  @click="deletePeer(peer)"
              >{{ deleting === peer.name ? '删除中…' : '删除' }}</button>
            </div>
          </div>
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
          <div class="import-actions">
            <button
                class="btn"
                type="button"
                :disabled="!newUrl.trim() || importing || deleting !== undefined"
                @click="importUrl"
            >{{ importing ? '导入中…' : '导入节点' }}</button>
          </div>
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
        <button
            class="btn btn--accent"
            type="button"
            :disabled="importing || deleting !== undefined || loading"
            @click="submit"
        >保存线路</button>
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

.field__help {
  font: var(--font-caption);
  color: var(--text-fill-secondary);
}

.field__help--warning {
  color: var(--system-fill-caution);
}

.peer-list {
  display: flex;
  flex-direction: column;
  max-height: 142px;
  border: 1px solid var(--control-stroke-default);
  border-radius: var(--control-corner-radius);
  overflow-y: auto;
}

.peer-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 34px;
  padding: 4px 6px 4px 10px;
  background: var(--control-fill-default);
}

.peer-row + .peer-row {
  border-top: 1px solid var(--divider-stroke);
}

.peer-row__name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font: var(--font-body);
  color: var(--text-fill-primary);
}

.peer-row__protocol {
  flex: 0 0 auto;
  font: var(--font-caption);
  color: var(--text-fill-tertiary);
}

.peer-row__delete {
  flex: 0 0 auto;
  min-width: 0;
  padding-inline: 8px;
}

.import-actions {
  display: flex;
  justify-content: flex-end;
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
