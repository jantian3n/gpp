//go:build windows

package main

import "github.com/wailsapp/wails/v2/pkg/options/windows"

// nativeChrome 表示窗口不使用系统标题栏，改由前端绘制 Fluent 标题栏。
//
// 前端在 src/composables/usePlatform.ts 里用 navigator.userAgent 判断平台来决定
// 是否渲染自绘标题栏；这个常量与那段判断必须保持一致，否则会出现
// "两条标题栏" 或者 "一条都没有"。
const nativeChrome = true

// platformWindowOptions 返回 Windows 专属的窗口选项。
//
// 这里刻意保持窗口"不透明"：不开 WindowIsTranslucent，也不用 BackdropType 云母材质。
//
// 原因是实测结论：一旦开启半透明，Wails 会给窗口加 WS_EX_NOREDIRECTIONBITMAP 并把
// 绘制交给 DWM 环绕合成，而在目标机器上 WebView2 的内容完全不会被合成进窗口 ——
// 窗口本身可见、子窗口（Chrome_RenderWidgetHostWidget 等）尺寸也正常，但屏幕上
// 一个像素都画不出来（连文字都没有）。同一个前端放在普通不透明窗口里渲染完全正常，
// 所以问题出在半透明这条合成路径上，而不是界面本身。
//
// 无边框、Win11 圆角与投影都保留（不设置 DisableFramelessWindowDecorations），
// 界面底色改用不透明的 Win11 基准底色，视觉效果与原生窗口一致。
func platformWindowOptions() *windows.Options {
	return &windows.Options{}
}
