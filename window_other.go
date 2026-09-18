//go:build !windows

package main

import "github.com/wailsapp/wails/v2/pkg/options/windows"

// nativeChrome 只在 Windows 上启用自绘标题栏：macOS 有关闭/最小化/缩放的
// 红黄绿灯，Linux 各桌面的窗口装饰也各不相同，无边框在这两个平台上都只会
// 让窗口失去系统提供的拖动、缩放和窗口管理器集成能力。
//
// 前端会据此只渲染内容区（见 src/composables/usePlatform.ts）。
const nativeChrome = false

// platformWindowOptions 在非 Windows 平台上没有对应概念，直接返回 nil，
// Wails 会跳过所有 Windows 专属的窗口处理。
func platformWindowOptions() *windows.Options { return nil }
