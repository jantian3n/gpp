<template>
  <div class="container">
    <div class="center">
      <n-space vertical>
        <n-space style="text-align: center">

          <n-progress
              type="circle"
              :height="24"
              :status="percentageRef<=25?'error':percentageRef<=50?'warning':percentageRef<=75?'info':'success'"
              :percentage="percentageRef"
          >
            <n-space vertical size="small">
              {{ percentageRef === 100 ? '加速完成' : starting ? '正在启动…' : percentageRef === 0 ? '未开始' : '正在加速' }}
              <n-space vertical size="small" v-if="showGameHttpInfo">
                <p @click="getList()">
                  Game:{{ gamePeer === null ? '未选择' : gamePeer.name }}
                  <n-gradient-text v-if="gamePeer && gamePeer.ping > 0"
                                   :type="gamePeer.ping<60?'success':gamePeer.ping<100?'warning':'error'">
                    {{ gamePeer.ping }}
                  </n-gradient-text>
                </p>
                <p @click="getList()">
                  Http:{{ httpPeer === null ? '未选择' : httpPeer.name }}
                  <n-gradient-text v-if="httpPeer && httpPeer.ping > 0"
                                   :type="httpPeer.ping<60?'success':httpPeer.ping<100?'warning':'error'">
                    {{ httpPeer.ping }}
                  </n-gradient-text>
                </p>
                <p v-if="noPeer" style="color: #7a7a7a">
                  还没有节点：点击下方按钮导入链接或订阅地址
                </p>
              </n-space>
              <n-space vertical size="small" v-if="showUpDowInfo">
                <!--                <p>-->
                <!--                  上传:-->
                <!--                  <n-gradient-text v-if="up" type="success">-->
                <!--                    {{ up / 1024 > 1024 ? (up / 1024 / 1024).toFixed(2) + 'MB' : (up / 1024).toFixed(2) + 'KB' }}-->
                <!--                  </n-gradient-text>-->
                <!--                </p>-->
                <p>
                  流量统计:
                  <n-gradient-text v-if="down" type="success">
                    {{ down / 1024 > 1024 ? (down / 1024 / 1024).toFixed(2) + 'MB' : (down / 1024).toFixed(2) + 'KB' }}
                  </n-gradient-text>
                  <span v-if="downRate">（{{ humanRate(downRate) }}/s）</span>
                </p>
              </n-space>
            </n-space>
          </n-progress>
        </n-space>
        <n-space vertical size="small" style="align-items: center">
          <span style="color: #7a7a7a; font-size: 12px">
            {{ coreLabel }}
          </span>
        </n-space>
        <n-space>
          <n-button :disabled="btnDisabled" @click="onMainButton()" style="margin-left: 110px">
            {{ btnText }}
          </n-button>
        </n-space>
        <n-gradient-text type="success" style="margin-left: 130px;margin-top: 35px">
          v1.4.7
        </n-gradient-text>
      </n-space>
      <div>
        <n-modal
            v-model:show="showModal"
            :mask-closable="false"
            preset="dialog"
            title="节点列表"
            positive-text="确认"
            negative-text="取消"
            @positive-click="submitCallback"
        >
          <n-select
              v-model:value="gameValue"
              vertical
              filterable
              :options="gameOpt"
              placeholder="请选择Game"
              value-field="val"
              label-field="name"
          />
          <br>
          <n-select
              v-model:value="httpValue"
              vertical
              filterable
              :options="httpOpt"
              placeholder="请选择Http"
              value-field="val"
              label-field="name"
          />
          <br>
          <n-input
              v-model:value="newUrl"
              type="textarea"
              placeholder="导入新连接"
          />
        </n-modal>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import {ref, defineComponent, Ref, reactive, onMounted, watch} from 'vue'
import {Add, List, SetPeer, Start, Status, Stop} from "../../wailsjs/go/main/App";
import {EventsOn} from "../../wailsjs/runtime";
import {SelectOption, SelectGroupOption} from 'naive-ui'
import {onBeforeMount} from "@vue/runtime-core";
import {useMessage} from 'naive-ui'

const percentageRef = ref(0)
const state = ref(false)
const btnText = ref('开始加速')
const btnDisabled = ref(false)
const showModal = ref(false)
const gameOpt = ref(Array<SelectOption | SelectGroupOption>())
const httpOpt = ref(Array<SelectOption | SelectGroupOption>())
const gameValue = ref()
const httpValue = ref()
const noPeer = ref(false)
const starting = ref(false)

const gamePeer: Ref<any> | null = ref(null)
const httpPeer: Ref<any> | null = ref(null)
const up = ref()
const down = ref()
const downRate = ref()
const coreLabel = ref('')

// humanRate 把字节/秒格式化成可读文本
const humanRate = (bytes: number) => {
  if (!bytes) return '0B'
  if (bytes > 1024 * 1024) return (bytes / 1024 / 1024).toFixed(2) + 'MB'
  if (bytes > 1024) return (bytes / 1024).toFixed(1) + 'KB'
  return bytes + 'B'
}

const showGameHttpInfo = ref(true)
const showUpDowInfo = ref(false)

const newUrl = ref()

let time = ref()
onMounted(() => {
  getStatus()
  time.value = setInterval(() => {
    getStatus()
  }, 1000);
  // 核心事件（另一端界面做的操作、订阅回退、自动切换节点等）立即提示，不用等轮询
  EventsOn('gpp:event', (event: any) => {
    if (!event || !event.message) return
    if (event.type === 'warning') {
      message.warning(event.message)
    } else if (event.type === 'started' || event.type === 'stopped') {
      message.info(event.message)
    }
  })
})

onBeforeMount(() => {
  clearInterval(time.value)
  time.value = null;
})

const message = useMessage()

const start = () => {
  btnDisabled.value = true
  starting.value = true
  showGameHttpInfo.value = false
  showUpDowInfo.value = true
  btnText.value = '正在启动…'
  percentageRef.value = 0
  Start().then(res => {
    starting.value = false
    if (res !== 'ok' && res !== 'running') {
      message.error(`加速失败:` + res)
      btnDisabled.value = false
      showUpDowInfo.value = false
      showGameHttpInfo.value = true
      btnText.value = '开始加速'
      return;
    }
    state.value = true
    // 进度只反映真实状态：启动成功才到 100%
    percentageRef.value = 100
    btnText.value = '结束加速'
    btnDisabled.value = false
  })
}
// onMainButton：没有节点时按钮直接打开导入/选择弹窗，避免"灰按钮 + 不知道怎么导入"
const onMainButton = () => {
  if (noPeer.value) {
    getList()
    return
  }
  if (state.value) {
    stop()
    return
  }
  start()
}
const stop = () => {
  Stop().then(res => {
    percentageRef.value = 0
    starting.value = false
    state.value = false
    showGameHttpInfo.value = true
    showUpDowInfo.value = false
    btnText.value = '开始加速'
    if (res !== 'ok' && res !== 'not running') {
      message.error('停止失败:' + res)
    }
  })
}
const getList = () => {
  showModal.value = true
  httpOpt.value = Array<SelectOption | SelectGroupOption>()
  gameOpt.value = Array<SelectOption | SelectGroupOption>()
  List().then(res => {
    res.forEach((item) => {
      if(item.name.startsWith("game")){
        return
      }
      httpOpt.value.push({
        name: peerLabel(item),
        val: item.name
      })
    })
  })
  List().then(res => {
    res.forEach((item) => {
      if(item.name.startsWith("http")){
        return
      }
      gameOpt.value.push({
        name: peerLabel(item),
        val: item.name
      })
    })
  })
}

// peerLabel 把延迟拼进选项名：测不到延迟的节点（例如只监听 UDP 的 hysteria2）显示"未测速"，
// 不再显示成 0ms 让人误以为它最快。
const peerLabel = (item: any) => {
  return item.name + (item.ping > 0 ? '-' + item.ping + 'ms' : '-未测速')
}


const getStatus = () => {
  Status().then(res => {
    // 后端的一次性提示（订阅更新失败、选中节点被自动切换等），提示一次即清空
    if (res.warning) {
      message.warning(res.warning)
    }
    downRate.value = res.down_rate
    // 说明"隧道由谁持有"：GUI 与 TUI 共用同一份状态，这里让用户看得见
    if (res.core_pid) {
      const who = res.core_kind === 'gui' ? '本窗口' : (res.core_kind === 'tui' ? '终端版 gpp-tui' : res.core_kind)
      coreLabel.value = `核心：${who}（PID ${res.core_pid}）· 配置：${res.config_path}`
    }
    if (res.game_peer !== null || res.http_peer !== null) {
      gamePeer.value = res.game_peer
      httpPeer.value = res.http_peer
      up.value = res.up
      down.value = res.down
      noPeer.value = false
      if (!starting.value) {
        btnDisabled.value = false
        btnText.value = state.value ? '结束加速' : '开始加速'
      }
      return;
    }
    noPeer.value = true
    btnText.value = '导入节点'
    btnDisabled.value = false
  })
}

const submitCallback = () => {
  if (newUrl.value !== undefined && gameValue.value !== undefined && httpValue.value !== undefined) {
    message.error('只能选择一种方式')
    newUrl.value = undefined;
    gameValue.value = undefined;
    httpValue.value = undefined;
    return
  }
  if (newUrl.value !== undefined) {
    Add(newUrl.value).then(res => {
      if (res === 'ok') {
        message.success('导入连接成功')
        newUrl.value = undefined;
        getList(); // 导入后立刻刷新列表，不用关掉弹窗再打开才能看到新节点
      } else {
        message.error(res)
      }
    });
  }
  if (gameValue.value !== undefined || httpValue.value !== undefined) {
    if (gameValue.value === undefined) {
      message.error('请选择Game节点')
      httpValue.value = undefined;
      return
    }
    if (httpValue.value === undefined) {
      message.error('请选择Http节点')
      gameValue.value = undefined;
      return
    }
    SetPeer(gameValue.value, httpValue.value).then(res => {
      if (res === 'ok') {
        message.success('设置节点成功')
        if (state.value) {
          // 加速中的实例不会热切换节点，必须重启才生效，这里明确告诉用户
          message.warning('当前正在加速：请先“结束加速”再重新开始，新节点才会生效')
        }
        gameValue.value = undefined;
        httpValue.value = undefined;
      } else {
        message.error(res)
      }
    });
  }
};


</script>

<style>
.container {
  display: flex;
  justify-content: center;
  align-items: center;
}

.center {
  /* 可以添加宽度、高度等样式 */
  margin-top: 20%;
  margin-left: -100px;
}


.n-progress-content {
  width: 300px;
  height: 300px;
}


.n-progress-content svg {
  width: 300px;
  height: 300px;
}
</style>