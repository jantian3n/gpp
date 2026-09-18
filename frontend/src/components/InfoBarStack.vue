<script lang="ts" setup>
import {useMessages} from '../composables/useMessages'

const {messages, dismiss} = useMessages()
</script>

<template>
  <!-- Win11 的 InfoBar：就地展开的一行提示，不用浮层遮挡内容 -->
  <div v-if="messages.length" class="infobar-stack">
    <div
        v-for="item in messages"
        :key="item.id"
        class="infobar"
        :class="`infobar--${item.severity}`"
        role="status"
    >
      <span class="infobar__icon">
        <svg v-if="item.severity === 'success'" viewBox="0 0 16 16" aria-hidden="true">
          <circle cx="8" cy="8" r="6.6" fill="none" stroke="currentColor" stroke-width="1.2"/>
          <path d="M4.9 8.3 7 10.4l4.2-4.6" fill="none" stroke="currentColor" stroke-width="1.4"
                stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
        <svg v-else-if="item.severity === 'warning'" viewBox="0 0 16 16" aria-hidden="true">
          <path d="M8 1.9 14.4 13H1.6z" fill="none" stroke="currentColor" stroke-width="1.2"
                stroke-linejoin="round"/>
          <path d="M8 6.1v3.3" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/>
          <circle cx="8" cy="11.3" r="0.9" fill="currentColor"/>
        </svg>
        <svg v-else-if="item.severity === 'error'" viewBox="0 0 16 16" aria-hidden="true">
          <circle cx="8" cy="8" r="6.6" fill="none" stroke="currentColor" stroke-width="1.2"/>
          <path d="M5.6 5.6 10.4 10.4M10.4 5.6 5.6 10.4" fill="none" stroke="currentColor"
                stroke-width="1.4" stroke-linecap="round"/>
        </svg>
        <svg v-else viewBox="0 0 16 16" aria-hidden="true">
          <circle cx="8" cy="8" r="6.6" fill="none" stroke="currentColor" stroke-width="1.2"/>
          <path d="M8 7.2v4.4" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/>
          <circle cx="8" cy="4.7" r="0.9" fill="currentColor"/>
        </svg>
      </span>
      <span class="infobar__text">{{ item.text }}</span>
      <button class="infobar__close" type="button" aria-label="关闭提示" title="关闭提示"
              @click="dismiss(item.id)">
        <svg viewBox="0 0 10 10" aria-hidden="true">
          <path d="M0.5 0.5 9.5 9.5M9.5 0.5 0.5 9.5" fill="none" stroke="currentColor" stroke-width="1"/>
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped>
.infobar-stack {
  display: flex;
  flex-direction: column;
  gap: 6px;
  flex: 0 0 auto;
}

.infobar {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 6px 10px 12px;
  border-radius: var(--control-corner-radius);
  border: 1px solid var(--infobar-stroke);
  background: var(--infobar-fill);
  color: var(--text-fill-primary);
  font: var(--font-caption);
}

.infobar--success {
  border-color: var(--system-fill-success-stroke);
  background: var(--system-fill-success-background);
}

.infobar--warning {
  border-color: var(--system-fill-caution-stroke);
  background: var(--system-fill-caution-background);
}

.infobar--error {
  border-color: var(--system-fill-critical-stroke);
  background: var(--system-fill-critical-background);
}

.infobar__icon {
  flex: 0 0 auto;
  width: 16px;
  height: 16px;
  margin-top: 1px;
}

.infobar--success .infobar__icon {
  color: var(--system-fill-success);
}

.infobar--warning .infobar__icon {
  color: var(--system-fill-caution);
}

.infobar--error .infobar__icon {
  color: var(--system-fill-critical);
}

.infobar--info .infobar__icon {
  color: var(--system-fill-attention);
}

.infobar__text {
  flex: 1 1 auto;
  min-width: 0;
  /* 后端会把多条提示用 \n 拼起来，这里保留换行 */
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font: var(--font-body);
}

.infobar__close {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 24px;
  padding: 0;
  border: 0;
  border-radius: var(--control-corner-radius);
  background: transparent;
  color: var(--text-fill-secondary);
  cursor: default;
}

.infobar__close:hover {
  background: var(--control-fill-secondary);
  color: var(--text-fill-primary);
}

.infobar__close:active {
  background: var(--control-fill-tertiary);
}

.infobar__close:focus-visible {
  outline: var(--focus-outline);
  outline-offset: -1px;
}

.infobar__close svg {
  width: 10px;
  height: 10px;
}
</style>
