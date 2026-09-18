import {ref} from 'vue'

// 消息用 Win11 的 InfoBar（就地展开的一行提示）而不是浮动的 toast：
// 窗口只有 360×520，浮层会盖住主要内容，InfoBar 更贴近系统应用的做法。
export type MessageSeverity = 'success' | 'warning' | 'error' | 'info'

export interface FluentMessage {
  id: number
  severity: MessageSeverity
  text: string
}

// 最多同时显示 3 条，避免后端反复报同一个问题时把窗口撑满
const MAX_MESSAGES = 3

const messages = ref<FluentMessage[]>([])
let nextId = 1

function dismiss(id: number) {
  messages.value = messages.value.filter(item => item.id !== id)
}

function push(severity: MessageSeverity, text: string, autoDismissMs = 0) {
  const content = (text ?? '').trim()
  if (!content) {
    return
  }
  // 同一条消息连着来（例如状态轮询每次都带同一个 warning）只保留一条
  if (messages.value.some(item => item.severity === severity && item.text === content)) {
    return
  }
  const id = nextId++
  messages.value = [...messages.value, {id, severity, text: content}].slice(-MAX_MESSAGES)
  if (autoDismissMs > 0) {
    setTimeout(() => dismiss(id), autoDismissMs)
  }
}

export function useMessages() {
  return {
    messages,
    dismiss,
    success: (text: string) => push('success', text, 4000),
    warning: (text: string) => push('warning', text),
    error: (text: string) => push('error', text),
    info: (text: string) => push('info', text),
  }
}
