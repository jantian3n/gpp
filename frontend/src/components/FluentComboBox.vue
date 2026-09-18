<script lang="ts" setup>
import {computed, nextTick, onBeforeUnmount, ref, watch} from 'vue'
import type {ComboOption} from '../types'

const props = withDefaults(defineProps<{
  modelValue?: string
  options: ComboOption[]
  placeholder?: string
  disabled?: boolean
  emptyText?: string
}>(), {
  placeholder: '未选择',
  disabled: false,
  emptyText: '没有可选项',
})

const emit = defineEmits<{(e: 'update:modelValue', value: string): void}>()

// 下拉浮层的最大高度；空间不够时会自动压缩或向上翻转
const POPUP_MAX_HEIGHT = 264
const POPUP_MIN_HEIGHT = 104
const POPUP_GAP = 4
// 视口边距，避免浮层贴到窗口边缘（无边框窗口贴边会看着很挤）
const VIEWPORT_MARGIN = 8

let uidSeed = 0

const uid = `combo-${++uidSeed}`
const open = ref(false)
const query = ref('')
const activeIndex = ref(-1)
const triggerRef = ref<HTMLButtonElement | null>(null)
const popupRef = ref<HTMLDivElement | null>(null)
const listRef = ref<HTMLDivElement | null>(null)
const searchRef = ref<HTMLInputElement | null>(null)
const popupStyle = ref<Record<string, string>>({})

// 选项少的时候不显示搜索框，保持原生 ComboBox 的克制感
const showSearch = computed(() => props.options.length > 8)

const selectedOption = computed(() =>
    props.options.find(item => item.value === props.modelValue))

const filtered = computed(() => {
  const keyword = query.value.trim().toLowerCase()
  if (!keyword) {
    return props.options
  }
  return props.options.filter(item =>
      item.label.toLowerCase().includes(keyword) ||
      (item.meta ?? '').toLowerCase().includes(keyword))
})

// positionPopup 用触发器的位置算浮层坐标。窗口尺寸固定，但内容区可能滚动，
// 所以打开时和每次滚动/缩放都重新算一遍，避免浮层和按钮错位。
function positionPopup() {
  const el = triggerRef.value
  if (!el) {
    return
  }
  const rect = el.getBoundingClientRect()
  const spaceBelow = window.innerHeight - rect.bottom - POPUP_GAP - VIEWPORT_MARGIN
  const spaceAbove = rect.top - POPUP_GAP - VIEWPORT_MARGIN
  const preferBelow = spaceBelow >= Math.min(160, spaceAbove)
  const available = preferBelow ? spaceBelow : spaceAbove
  const maxHeight = Math.max(POPUP_MIN_HEIGHT, Math.min(POPUP_MAX_HEIGHT, available))

  popupStyle.value = {
    left: `${Math.round(rect.left)}px`,
    width: `${Math.round(rect.width)}px`,
    maxHeight: `${Math.round(maxHeight)}px`,
    ...(preferBelow
        ? {top: `${Math.round(rect.bottom + POPUP_GAP)}px`}
        : {bottom: `${Math.round(window.innerHeight - rect.top + POPUP_GAP)}px`}),
  }
}

function scrollActiveIntoView() {
  const list = listRef.value
  if (!list) {
    return
  }
  const node = list.children[activeIndex.value] as HTMLElement | undefined
  node?.scrollIntoView({block: 'nearest'})
}

function setActive(index: number) {
  activeIndex.value = index
}

function moveActive(delta: number) {
  if (!filtered.value.length) {
    return
  }
  const last = filtered.value.length - 1
  const next = activeIndex.value < 0
      ? (delta > 0 ? 0 : last)
      : Math.min(last, Math.max(0, activeIndex.value + delta))
  activeIndex.value = next
  nextTick(scrollActiveIntoView)
}

function choose(option: ComboOption) {
  emit('update:modelValue', option.value)
  close()
}

function close() {
  if (!open.value) {
    return
  }
  open.value = false
  query.value = ''
  activeIndex.value = -1
  detachListeners()
}

function openPopup() {
  if (props.disabled) {
    return
  }
  open.value = true
  query.value = ''
  const index = filtered.value.findIndex(item => item.value === props.modelValue)
  activeIndex.value = index
  attachListeners()
  void nextTick(() => {
    positionPopup()
    if (showSearch.value) {
      searchRef.value?.focus()
    } else {
      scrollActiveIntoView()
    }
  })
}

function toggle() {
  if (open.value) {
    close()
  } else {
    openPopup()
  }
}

function onKeydown(event: KeyboardEvent) {
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault()
      moveActive(1)
      break
    case 'ArrowUp':
      event.preventDefault()
      moveActive(-1)
      break
    case 'Home':
      event.preventDefault()
      activeIndex.value = filtered.value.length ? 0 : -1
      nextTick(scrollActiveIntoView)
      break
    case 'End':
      event.preventDefault()
      activeIndex.value = filtered.value.length - 1
      nextTick(scrollActiveIntoView)
      break
    case 'Enter': {
      event.preventDefault()
      const option = filtered.value[activeIndex.value]
      if (option) {
        choose(option)
      }
      break
    }
    case 'Escape':
      event.preventDefault()
      close()
      triggerRef.value?.focus()
      break
    case 'Tab':
      close()
      break
  }
}

function onDocumentPointerDown(event: PointerEvent) {
  const target = event.target as Node | null
  if (!target) {
    return
  }
  if (triggerRef.value?.contains(target) || popupRef.value?.contains(target)) {
    return
  }
  close()
}

function onWindowChange() {
  if (open.value) {
    positionPopup()
  }
}

function attachListeners() {
  document.addEventListener('pointerdown', onDocumentPointerDown, true)
  window.addEventListener('resize', onWindowChange)
  window.addEventListener('scroll', onWindowChange, true)
}

function detachListeners() {
  document.removeEventListener('pointerdown', onDocumentPointerDown, true)
  window.removeEventListener('resize', onWindowChange)
  window.removeEventListener('scroll', onWindowChange, true)
}

watch(() => props.disabled, value => {
  if (value) {
    close()
  }
})

// 选项列表在打开状态下变化（例如导入后刷新）时重新定位
watch(() => props.options.length, () => {
  if (open.value) {
    void nextTick(positionPopup)
  }
})

onBeforeUnmount(detachListeners)
</script>

<template>
  <div class="combo">
    <button
        ref="triggerRef"
        class="combo__trigger"
        :class="{'combo__trigger--open': open, 'combo__trigger--placeholder': !selectedOption}"
        type="button"
        role="combobox"
        :aria-expanded="open"
        :aria-controls="`${uid}-list`"
        :aria-disabled="disabled"
        :disabled="disabled"
        @click="toggle"
        @keydown="onKeydown"
    >
      <span class="combo__value">{{ selectedOption ? selectedOption.label : placeholder }}</span>
      <span v-if="selectedOption?.meta" class="combo__value-meta" :class="`tone--${selectedOption.tone ?? 'unknown'}`">
        {{ selectedOption.meta }}
      </span>
      <svg class="combo__chevron" viewBox="0 0 12 12" aria-hidden="true">
        <path d="M2 4.4 6 8.4l4-4" fill="none" stroke="currentColor" stroke-width="1.2"
              stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
    </button>

    <!-- 浮层用 position: fixed，避免被对话框的滚动容器裁掉 -->
    <div
        v-if="open"
        ref="popupRef"
        class="combo__popup"
        :style="popupStyle"
        @keydown="onKeydown"
    >
      <div v-if="showSearch" class="combo__search">
        <svg class="combo__search-icon" viewBox="0 0 16 16" aria-hidden="true">
          <circle cx="7" cy="7" r="4.4" fill="none" stroke="currentColor" stroke-width="1.2"/>
          <path d="M10.4 10.4 14 14" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
        </svg>
        <input
            ref="searchRef"
            v-model="query"
            class="combo__search-input"
            type="text"
            placeholder="搜索"
            spellcheck="false"
            autocomplete="off"
        />
      </div>
      <div :id="`${uid}-list`" ref="listRef" class="combo__list" role="listbox">
        <div v-if="!filtered.length" class="combo__empty">{{ emptyText }}</div>
        <button
            v-for="(option, index) in filtered"
            :key="option.value"
            class="combo__option"
            :class="{'combo__option--active': index === activeIndex}"
            type="button"
            role="option"
            :aria-selected="option.value === modelValue"
            tabindex="-1"
            @click="choose(option)"
            @pointermove="setActive(index)"
        >
          <span class="combo__option-label">{{ option.label }}</span>
          <span v-if="option.meta" class="combo__option-meta" :class="`tone--${option.tone ?? 'unknown'}`">
            {{ option.meta }}
          </span>
          <svg v-if="option.value === modelValue" class="combo__check" viewBox="0 0 12 12" aria-hidden="true">
            <path d="M2.2 6.4 4.8 9l5-5.4" fill="none" stroke="currentColor" stroke-width="1.4"
                  stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.combo {
  position: relative;
}

/* Win11 ComboBox：默认 ControlFill 底 + 细边框，底边略深；打开时底边变成强调色 */
.combo__trigger {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  height: 32px;
  padding: 0 8px 0 11px;
  border: 1px solid var(--control-stroke-default);
  border-bottom-color: var(--control-stroke-secondary);
  border-radius: var(--control-corner-radius);
  background: var(--control-fill-default);
  color: var(--text-fill-primary);
  font: var(--font-body);
  text-align: left;
  cursor: default;
}

.combo__trigger:hover {
  background: var(--control-fill-secondary);
}

.combo__trigger:active {
  background: var(--control-fill-tertiary);
}

.combo__trigger:focus-visible {
  outline: var(--focus-outline);
  outline-offset: 1px;
}

.combo__trigger--open {
  background: var(--control-fill-secondary);
  box-shadow: inset 0 -1px 0 0 var(--accent-fill-default);
}

.combo__trigger:disabled {
  background: var(--control-fill-disabled);
  border-color: var(--control-stroke-default);
  color: var(--text-fill-disabled);
}

.combo__trigger--placeholder .combo__value {
  color: var(--text-fill-secondary);
}

.combo__value {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.combo__value-meta,
.combo__option-meta {
  flex: 0 0 auto;
  font: var(--font-caption);
  /* 颜色由全局的 .tone--* 工具类给出（见 fluent.css），
     这里不能设基础色，否则会把语义色盖掉。 */
}

.combo__chevron {
  flex: 0 0 auto;
  width: 12px;
  height: 12px;
  color: var(--text-fill-secondary);
}

.combo__popup {
  position: fixed;
  z-index: 2000;
  display: flex;
  flex-direction: column;
  padding: 4px;
  border: 1px solid var(--surface-stroke-flyout);
  border-radius: var(--overlay-corner-radius);
  background: var(--acrylic-fill);
  box-shadow: var(--flyout-shadow);
  backdrop-filter: var(--backdrop-filter);
  overflow: hidden;
}

.combo__search {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 0 0 auto;
  margin-bottom: 4px;
  padding: 0 8px;
  height: 28px;
  border-radius: var(--control-corner-radius);
  background: var(--control-fill-default);
  border: 1px solid var(--control-stroke-default);
}

.combo__search-icon {
  flex: 0 0 auto;
  width: 14px;
  height: 14px;
  color: var(--text-fill-secondary);
}

.combo__search-input {
  flex: 1 1 auto;
  min-width: 0;
  border: 0;
  background: transparent;
  color: var(--text-fill-primary);
  font: var(--font-body);
  outline: none;
}

.combo__search-input::placeholder {
  color: var(--text-fill-tertiary);
}

.combo__list {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
}

.combo__option {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 32px;
  padding: 0 8px 0 11px;
  border: 0;
  border-radius: var(--control-corner-radius);
  background: transparent;
  color: var(--text-fill-primary);
  font: var(--font-body);
  text-align: left;
  cursor: default;
}

.combo__option--active {
  background: var(--control-fill-secondary);
}

.combo__option--active:active {
  background: var(--control-fill-tertiary);
}

.combo__option-label {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.combo__check {
  flex: 0 0 auto;
  width: 12px;
  height: 12px;
  color: var(--accent-fill-default);
}

.combo__empty {
  padding: 10px 11px;
  color: var(--text-fill-secondary);
  font: var(--font-body);
}
</style>
