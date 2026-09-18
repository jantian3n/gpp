<script lang="ts" setup>
import {WindowHide, WindowMinimise} from '../../wailsjs/runtime'
import appIcon from '../assets/images/app-icon.png'

withDefaults(defineProps<{ title?: string }>(), {title: 'gpp'})
</script>

<template>
  <!-- Win11 原生标题栏：32px 高，拖动区域交给 Wails 注入的 --wails-draggable
       （对应 options.App 的 CSSDragProperty 默认值）。
       Wails 只在事件目标自身上取这个属性，而自定义属性会继承，所以标题栏上的
       图标/文字天然也是拖动区域，按钮必须显式写回 non-drag 才能正常点击。 -->
  <header class="titlebar">
    <img :src="appIcon" alt="" class="titlebar__icon" draggable="false"/>
    <span class="titlebar__title">{{ title }}</span>
    <div class="titlebar__captions">
      <button
          class="caption"
          type="button"
          aria-label="最小化"
          title="最小化"
          @click="WindowMinimise()"
      >
        <svg class="caption__glyph" viewBox="0 0 10 10" aria-hidden="true">
          <path d="M0 4.5h10v1H0z"/>
        </svg>
      </button>
      <!-- gpp 是托盘程序：点 X 与系统标题栏的行为一致（HideWindowOnClose），
           只是隐藏窗口，隧道继续跑；真正退出在托盘菜单里。
           Wails v2 的运行时没有 WindowClose，隐藏窗口就是它的等价行为。 -->
      <button
          class="caption caption--close"
          type="button"
          aria-label="关闭"
          title="关闭（隐藏到托盘）"
          @click="WindowHide()"
      >
        <svg class="caption__glyph caption__glyph--stroke" viewBox="0 0 10 10" aria-hidden="true">
          <path d="M0.5 0.5 9.5 9.5M9.5 0.5 0.5 9.5"/>
        </svg>
      </button>
    </div>
  </header>
</template>

<style scoped>
.titlebar {
  display: flex;
  align-items: center;
  flex: 0 0 auto;
  height: var(--titlebar-height);
  padding-left: 12px;
  /* 材质（Mica）直接透上来，标题栏自己不上色，和 Win11 应用一致 */
  background: transparent;
  --wails-draggable: drag;
  user-select: none;
}

.titlebar__icon {
  width: 16px;
  height: 16px;
  flex: 0 0 auto;
  margin-right: 10px;
  pointer-events: none;
}

.titlebar__title {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font: var(--font-caption);
  color: var(--text-fill-primary);
  pointer-events: none;
}

.titlebar__captions {
  display: flex;
  align-self: stretch;
  flex: 0 0 auto;
}

.caption {
  /* 按钮不拖动：自定义属性会从标题栏继承下来，这里必须显式覆盖 */
  --wails-draggable: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: var(--caption-button-width);
  height: 100%;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--text-fill-primary);
  cursor: default;
}

.caption:hover {
  background: var(--caption-hover-fill);
}

.caption:active {
  background: var(--caption-pressed-fill);
}

.caption:focus-visible {
  outline: var(--focus-outline);
  outline-offset: -2px;
}

.caption--close:hover {
  background: var(--caption-close-hover-fill);
}

.caption--close:active {
  background: var(--caption-close-pressed-fill);
}

/* 关闭按钮悬停时是红底，图标要跟着变成白色 */
.caption--close:hover,
.caption--close:active {
  color: #ffffff;
}

.caption__glyph {
  width: 10px;
  height: 10px;
  fill: currentColor;
}

.caption__glyph--stroke {
  fill: none;
  stroke: currentColor;
  stroke-width: 1;
}
</style>
